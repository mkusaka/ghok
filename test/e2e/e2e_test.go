//go:build unix

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/protocol"
	"go.yaml.in/yaml/v3"
)

var ghokBinary, fakeBinary string

func TestMain(m *testing.M) {
	ghokPath, ghokSet := os.LookupEnv("GHOK_E2E_GHOK_BINARY")
	fakePath, fakeSet := os.LookupEnv("GHOK_E2E_FAKEGH_BINARY")
	if ghokSet || fakeSet {
		if !ghokSet || !fakeSet || ghokPath == "" || fakePath == "" {
			fmt.Fprintln(os.Stderr, "GHOK_E2E_GHOK_BINARY and GHOK_E2E_FAKEGH_BINARY must both name prebuilt executable files")
			os.Exit(1)
		}
		for _, target := range []struct {
			name, path string
			binary     *string
		}{{"GHOK_E2E_GHOK_BINARY", ghokPath, &ghokBinary}, {"GHOK_E2E_FAKEGH_BINARY", fakePath, &fakeBinary}} {
			path, err := filepath.Abs(target.path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", target.name, err)
				os.Exit(1)
			}
			info, err := os.Stat(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", target.name, err)
				os.Exit(1)
			}
			if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
				fmt.Fprintf(os.Stderr, "%s: %s is not a regular executable file\n", target.name, path)
				os.Exit(1)
			}
			*target.binary = path
		}
		os.Exit(m.Run())
	}
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	dir, err := os.MkdirTemp("", "ghok-e2e-build-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ghokBinary, fakeBinary = filepath.Join(dir, "ghok"), filepath.Join(dir, "fakegh")
	for _, target := range []struct{ path, pkg string }{{ghokBinary, "./cmd/ghok"}, {fakeBinary, "./test/fakegh"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		// Avoid competing with concurrently running package/race suites for
		// compiler workers; fixture builds are setup, not a performance test.
		args := []string{"build", "-p=1", "-o", target.path}
		if target.pkg == "./cmd/ghok" {
			args = append(args, "-ldflags=-X main.version=e2e-version -X main.commit=e2e-commit")
		}
		args = append(args, target.pkg)
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOWORK=off")
		output, buildErr := cmd.CombinedOutput()
		cancel()
		if buildErr != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", target.pkg, buildErr, output)
			_ = os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type sandbox struct {
	t                                 *testing.T
	root, cwd, bin, real, record, cfg string
	env                               map[string]string
}

type invocationRecord struct {
	Argv0       string   `json:"argv0"`
	Argv        []string `json:"argv"`
	Env         []string `json:"env"`
	Cwd         string   `json:"cwd"`
	StdinBase64 string   `json:"stdinBase64"`
	StdinSHA256 string   `json:"stdinSHA256"`
	StdinLength int      `json:"stdinLength"`
	PID         int      `json:"pid"`
}

type outcome struct {
	stdout, stderr []byte
	err            error
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	root := t.TempDir()
	s := &sandbox{t: t, root: root, cwd: filepath.Join(root, "work"), bin: filepath.Join(root, "bin"), real: filepath.Join(root, "real", "gh"), record: filepath.Join(root, "gh.jsonl"), cfg: filepath.Join(root, "config", "config.yaml")}
	for _, dir := range []string{s.cwd, s.bin, filepath.Dir(s.real), filepath.Dir(s.cfg), filepath.Join(root, "home"), filepath.Join(root, "gh-config"), filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile(fakeBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(s.real, body, 0700); err != nil {
		t.Fatal(err)
	}
	s.env = map[string]string{"HOME": filepath.Join(root, "home"), "XDG_CONFIG_HOME": filepath.Join(root, "xdg"), "GH_CONFIG_DIR": filepath.Join(root, "gh-config"), "GHOK_CONFIG_DIR": filepath.Dir(s.cfg), "PATH": s.bin + string(os.PathListSeparator) + filepath.Dir(s.real), "TMPDIR": filepath.Join(root, "tmp"), "FAKEGH_RECORD": s.record, "LANG": "C", "TERM": "xterm"}
	s.configure(nil)
	result := s.management(nil, "shim", "install", "--bin-dir", s.bin)
	result.code(t, 0)
	info, err := os.Lstat(filepath.Join(s.bin, "gh"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("enable did not install symlink: %v", err)
	}
	return s
}

func (s *sandbox) configure(change func(*config.Config)) {
	s.t.Helper()
	if err := os.Remove(s.cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.t.Fatal(err)
	}
	c, err := config.Load(s.cfg)
	if err != nil {
		s.t.Fatal(err)
	}
	no := false
	c.Logging.Enabled = &no
	if change != nil {
		change(c)
	}
	c.ApplyDefaults()
	body, err := yaml.Marshal(c)
	if err != nil {
		s.t.Fatal(err)
	}
	if err = os.WriteFile(s.cfg, body, 0600); err != nil {
		s.t.Fatal(err)
	}
}

func (s *sandbox) command(path string, input []byte, args ...string) (*exec.Cmd, context.CancelFunc) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = s.cwd
	cmd.Env = s.environ()
	cmd.Stdin = bytes.NewReader(input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 3 * time.Second
	return cmd, cancel
}

func (s *sandbox) environ() []string {
	env := make([]string, 0, len(s.env))
	for key, value := range s.env {
		env = append(env, key+"="+value)
	}
	return env
}

func (s *sandbox) run(path string, input []byte, args ...string) outcome {
	s.t.Helper()
	cmd, cancel := s.command(path, input, args...)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return outcome{stdout.Bytes(), stderr.Bytes(), err}
}

func (s *sandbox) gh(input []byte, args ...string) outcome {
	return s.run(filepath.Join(s.bin, "gh"), input, args...)
}
func (s *sandbox) management(input []byte, args ...string) outcome {
	return s.run(ghokBinary, input, args...)
}

func (o outcome) code(t *testing.T, want int) {
	t.Helper()
	got := 0
	if o.err != nil {
		var exit *exec.ExitError
		if !errors.As(o.err, &exit) {
			t.Fatalf("command error: %v; stderr=%s", o.err, o.stderr)
		}
		got = exit.ExitCode()
	}
	if got != want {
		t.Fatalf("exit=%d, want=%d; stderr=%s", got, want, o.stderr)
	}
}

func (o outcome) signaled(t *testing.T, want syscall.Signal) {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(o.err, &exit) {
		t.Fatalf("expected signal %v, got %v (%s)", want, o.err, o.stderr)
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != want {
		t.Fatalf("wait status=%v, want signal %v", exit.Sys(), want)
	}
}

func (s *sandbox) records() []invocationRecord {
	s.t.Helper()
	data, err := os.ReadFile(s.record)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		s.t.Fatal(err)
	}
	var records []invocationRecord
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var value invocationRecord
		if err = json.Unmarshal(line, &value); err != nil {
			s.t.Fatal(err)
		}
		records = append(records, value)
	}
	return records
}

func (s *sandbox) onlyRecord(args []string, input []byte) invocationRecord {
	s.t.Helper()
	records := s.records()
	if len(records) != 1 {
		s.t.Fatalf("real gh invocations=%d, want 1", len(records))
	}
	r := records[0]
	if !reflect.DeepEqual(r.Argv, args) {
		s.t.Fatalf("argv=%q, want=%q", r.Argv, args)
	}
	cwd, _ := filepath.EvalSymlinks(s.cwd)
	if r.Cwd != cwd {
		s.t.Fatalf("cwd=%q, want=%q", r.Cwd, cwd)
	}
	body, err := base64.StdEncoding.DecodeString(r.StdinBase64)
	if err != nil {
		s.t.Fatal(err)
	}
	sum := sha256.Sum256(input)
	if !bytes.Equal(body, input) || r.StdinLength != len(input) || r.StdinSHA256 != hex.EncodeToString(sum[:]) {
		s.t.Fatalf("stdin changed: length=%d, want=%d", r.StdinLength, len(input))
	}
	return r
}

func (s *sandbox) absentRecord() {
	s.t.Helper()
	if records := s.records(); len(records) != 0 {
		s.t.Fatalf("real gh unexpectedly executed %d times", len(records))
	}
}

func (s *sandbox) hook(id, phase string) config.Hook {
	return config.Hook{ID: id, Phase: phase, Command: []string{fakeBinary}, Env: map[string]string{"FAKEGH_MODE": "hook", "FAKEGH_HOOK_RECORD": filepath.Join(s.root, id+".json")}}
}

func (s *sandbox) hookInput(id string) protocol.Input {
	s.t.Helper()
	data, err := os.ReadFile(filepath.Join(s.root, id+".json"))
	if err != nil {
		s.t.Fatal(err)
	}
	var input protocol.Input
	if err = json.Unmarshal(data, &input); err != nil {
		s.t.Fatal(err)
	}
	if input.SchemaVersion != 1 || input.HookID != id || input.EventID == "" {
		s.t.Fatalf("invalid hook envelope: %+v", input)
	}
	return input
}

func envMap(entries []string) map[string]string {
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, _ := strings.Cut(entry, "=")
		result[key] = value
	}
	return result
}

func setBytes(env map[string]string, key string, body []byte) {
	env[key] = base64.StdEncoding.EncodeToString(body)
}

func TestTransparentExecution(t *testing.T) {
	for _, supervised := range []bool{false, true} {
		for _, input := range [][]byte{nil, {0, 'a', '\n', 255, 0}} {
			for _, exit := range []int{0, 1, 2, 42} {
				t.Run(fmt.Sprintf("supervised=%v/stdin=%d/exit=%d", supervised, len(input), exit), func(t *testing.T) {
					s := newSandbox(t)
					if supervised {
						s.configure(func(c *config.Config) { c.Hooks = []config.Hook{s.hook("post", "post")} })
					}
					args := []string{"version", "", "white space", "tab\tline\n", "π雪", "-leading", "*?[x]", "--", "quote\"'\\"}
					out, errout := []byte{0, 'o', 255, '\n'}, []byte{'e', 0, 254, '\n'}
					setBytes(s.env, "FAKEGH_STDOUT_B64", out)
					setBytes(s.env, "FAKEGH_STDERR_B64", errout)
					s.env["FAKEGH_EXIT"] = strconv.Itoa(exit)
					s.env["CUSTOM_INHERITED"] = "spaces=and\nnewlines π"
					result := s.gh(input, args...)
					result.code(t, exit)
					if !bytes.Equal(result.stdout, out) || !bytes.Equal(result.stderr, errout) {
						t.Fatal("stdout/stderr changed")
					}
					r := s.onlyRecord(args, input)
					expected := make(map[string]string, len(s.env)+1)
					for k, v := range s.env {
						expected[k] = v
					}
					expected["GHOK_DEPTH"] = "1"
					if got := envMap(r.Env); !reflect.DeepEqual(got, expected) {
						t.Fatalf("environment changed:\ngot=%v\nwant=%v", got, expected)
					}
					if supervised {
						post := s.hookInput("post")
						if post.Result == nil || post.Result.ExitCode != exit || post.Result.Signal != "" {
							t.Fatalf("post result=%+v", post.Result)
						}
					}
				})
			}
		}
	}
}

func TestLargeBinaryOutput(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(fmt.Sprintf("capture=%v", capture), func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				h := s.hook("post", "post")
				h.CaptureOutput = capture
				c.Hooks = []config.Hook{h}
				c.Runtime.CaptureLimit = 3 << 20
			})
			// Files avoid platform ARG_MAX limits for environment values.
			out := bytes.Repeat([]byte{0, 1, 2, 255, '\n'}, 400000)
			errout := bytes.Repeat([]byte{'e', 0, 254}, 400000)
			for key, body := range map[string][]byte{"FAKEGH_STDOUT_FILE": out, "FAKEGH_STDERR_FILE": errout} {
				path := filepath.Join(s.root, key)
				if err := os.WriteFile(path, body, 0600); err != nil {
					t.Fatal(err)
				}
				s.env[key] = path
			}
			result := s.gh(nil, "version")
			result.code(t, 0)
			if !bytes.Equal(result.stdout, out) || !bytes.Equal(result.stderr, errout) {
				t.Fatal("large output changed")
			}
			s.onlyRecord([]string{"version"}, nil)
			post := s.hookInput("post")
			if capture {
				assertCapture(t, post.Result.Stdout, out, false)
				assertCapture(t, post.Result.Stderr, errout, false)
			} else if post.Result.Stdout != nil || post.Result.Stderr != nil {
				t.Fatal("uncaptured hook received output")
			}
		})
	}
}

func assertCapture(t *testing.T, got *protocol.Output, want []byte, truncated bool) {
	t.Helper()
	if got == nil {
		t.Fatal("missing capture")
	}
	body, err := base64.StdEncoding.DecodeString(got.Base64)
	if err != nil || !bytes.Equal(body, want) || got.Truncated != truncated {
		t.Fatalf("invalid capture: %+v, decode=%v", got, err)
	}
	if !truncated && got.Size != int64(len(want)) {
		t.Fatalf("capture size=%d, want=%d", got.Size, len(want))
	}
}

func TestMixedCaptureHooksAndLimit(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		a, b := s.hook("capture", "post"), s.hook("plain", "post")
		a.CaptureOutput = true
		c.Hooks = []config.Hook{a, b}
		c.Runtime.CaptureLimit = 8
	})
	body := []byte("0123456789abcdef")
	setBytes(s.env, "FAKEGH_STDOUT_B64", body)
	result := s.gh(nil, "version")
	result.code(t, 0)
	if !bytes.Equal(result.stdout, body) {
		t.Fatal("capture truncated caller output")
	}
	s.onlyRecord([]string{"version"}, nil)
	capture, plain := s.hookInput("capture"), s.hookInput("plain")
	assertCapture(t, capture.Result.Stdout, body[:8], true)
	if capture.Result.Stdout.Size != int64(len(body)) {
		t.Fatal("capture lost total byte count")
	}
	if plain.Result.Stdout != nil || plain.Result.Stderr != nil {
		t.Fatal("noncapturing hook received another hook's capture")
	}
}

func TestSignalExitStatus(t *testing.T) {
	for _, supervised := range []bool{false, true} {
		for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
			t.Run(fmt.Sprintf("supervised=%v/signal=%d", supervised, sig), func(t *testing.T) {
				s := newSandbox(t)
				if supervised {
					s.configure(func(c *config.Config) { c.Hooks = []config.Hook{s.hook("post", "post")} })
				}
				s.env["FAKEGH_SIGNAL"] = strconv.Itoa(int(sig))
				result := s.gh(nil, "version")
				result.signaled(t, sig)
				s.onlyRecord([]string{"version"}, nil)
				if supervised {
					post := s.hookInput("post")
					want := "SIGTERM"
					if sig == syscall.SIGINT {
						want = "SIGINT"
					}
					if post.Result == nil || post.Result.ExitCode != -1 || post.Result.Signal != want {
						t.Fatalf("signal post result=%+v", post.Result)
					}
				}
			})
		}
	}
}

func TestStartErrors(t *testing.T) {
	for _, supervised := range []bool{false, true} {
		for _, kind := range []string{"missing-interpreter", "invalid-image"} {
			t.Run(fmt.Sprintf("supervised=%v/%s", supervised, kind), func(t *testing.T) {
				s := newSandbox(t)
				if supervised {
					s.configure(func(c *config.Config) { c.Hooks = []config.Hook{s.hook("post", "post")} })
				}
				path := filepath.Join(s.root, "bad-gh")
				want := 127
				body := []byte("#!/no/such/ghok-e2e-interpreter\n")
				if kind == "invalid-image" {
					body = []byte("invalid executable image")
					want = 126
				}
				if err := os.WriteFile(path, body, 0700); err != nil {
					t.Fatal(err)
				}
				s.env["GHOK_REAL_GH"] = path
				result := s.gh(nil, "version")
				result.code(t, want)
				s.absentRecord()
				if !bytes.HasPrefix(result.stderr, []byte("ghok:")) {
					t.Fatal("missing start error diagnostic")
				}
				if supervised {
					input := s.hookInput("post")
					if input.Result == nil || input.Result.StartError == "" || input.Result.ExitCode != -1 {
						t.Fatalf("start error post result=%+v", input.Result)
					}
				}
			})
		}
	}
}

func TestMissingRealGhConciseWrapperAndDetailedDoctor(t *testing.T) {
	s := newSandbox(t)
	missingDir := filepath.Join(s.root, "missing-bin")
	shim := filepath.Join(s.bin, "gh")
	missing := filepath.Join(missingDir, "gh")
	s.env["PATH"] = missingDir + string(os.PathListSeparator) + s.bin
	result := s.gh(nil, "version")
	result.code(t, 127)
	s.absentRecord()
	if !bytes.HasPrefix(result.stderr, []byte("ghok:")) || !bytes.Contains(result.stderr, []byte(shim)) || bytes.Count(result.stderr, []byte("\n")) != 1 {
		t.Fatalf("missing-gh diagnostic=%q", result.stderr)
	}
	for _, noisy := range []string{missingDir, "lstat", "no such file or directory"} {
		if bytes.Contains(result.stderr, []byte(noisy)) {
			t.Fatalf("wrapper dumped PATH discovery detail %q: %s", noisy, result.stderr)
		}
	}
	result = s.management(nil, "doctor", "--json")
	result.code(t, 1)
	var checks []struct {
		Name   string
		Detail string
	}
	if err := json.Unmarshal(result.stdout, &checks); err != nil {
		t.Fatal(err)
	}
	for _, check := range checks {
		if check.Name == "real-gh" {
			if !strings.Contains(check.Detail, missing) || !strings.Contains(check.Detail, shim) || !strings.Contains(check.Detail, "no such file or directory") {
				t.Fatalf("doctor lost candidate diagnostics: %+v", check)
			}
			return
		}
	}
	t.Fatalf("doctor omitted real-gh check: %+v", checks)
}

// The suite exercises the binary releases ship: built without cgo.
func TestBinaryIsCGOFree(t *testing.T) {
	info, err := buildinfo.ReadFile(ghokBinary)
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range info.Settings {
		if setting.Key == "CGO_ENABLED" && setting.Value == "0" {
			return
		}
	}
	t.Fatalf("%s was built with cgo: %+v", ghokBinary, info.Settings)
}

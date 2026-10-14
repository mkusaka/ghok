package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func checkNamed(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("missing check %q: %+v", name, checks)
	return Check{}
}
func doctorFixture(t *testing.T) DoctorOptions {
	t.Helper()
	o := enableFixture(t, "ghok")
	result, err := Enable(o)
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	return DoctorOptions{Self: o.Self, Version: "test", PathEnv: o.PathEnv, BinDir: filepath.Dir(result.ShimPath), StateDir: o.StateDir, Env: []string{"HOME=" + filepath.Dir(o.Self), "XDG_CONFIG_HOME=" + filepath.Join(filepath.Dir(o.Self), "config"), "PATH=/nonexistent"}, Stdin: stdin}
}

func TestDoctorHealthyAndRecursionRisks(t *testing.T) {
	o := doctorFixture(t)
	checks := Checks(o)
	for _, name := range []string{"ghok", "real-gh", "path-precedence", "shim-ownership", "recursion-risk"} {
		if c := checkNamed(t, checks, name); c.Status != StatusOK {
			t.Errorf("%+v", c)
		}
	}
	if c := checkNamed(t, checks, "gh-version"); c.Status != StatusInfo || !strings.Contains(c.Detail, "2.102.0") {
		t.Errorf("%+v", c)
	}
	if c := checkNamed(t, checks, "platform-process"); !strings.Contains(c.Detail, "stdin tty=false") || !strings.Contains(c.Detail, "foreground process group") {
		t.Errorf("%+v", c)
	}
	shim := filepath.Join(o.BinDir, "gh")
	o.Env = append(o.Env, "GH_PATH="+shim)
	if c := checkNamed(t, Checks(o), "recursion-risk"); c.Status != StatusFail || !strings.Contains(c.Detail, "GH_PATH") {
		t.Errorf("%+v", c)
	}
	o.Env = []string{"GHOK_DEPTH=4"}
	if c := checkNamed(t, Checks(o), "recursion-risk"); c.Status != StatusFail || !strings.Contains(c.Detail, "recursion limit") {
		t.Errorf("%+v", c)
	}
	o.Env = []string{"GHOK_DEPTH=3"}
	if c := checkNamed(t, Checks(o), "recursion-risk"); c.Status != StatusOK {
		t.Errorf("%+v", c)
	}
	o.Override = shim
	checks = Checks(o)
	if c := checkNamed(t, checks, "real-gh"); c.Status != StatusFail {
		t.Errorf("%+v", c)
	}
	if c := checkNamed(t, checks, "recursion-risk"); c.Status != StatusFail || !strings.Contains(c.Detail, "override") {
		t.Errorf("%+v", c)
	}
}

func TestDoctorRecordedCustomShim(t *testing.T) {
	for _, kind := range []string{"owned", "absent", "foreign-file", "foreign-symlink"} {
		t.Run(kind, func(t *testing.T) {
			o := doctorFixture(t)
			shim := filepath.Join(o.BinDir, "gh")
			// The caller supplies the default directory, not the custom install.
			o.BinDir = filepath.Join(filepath.Dir(o.Self), ".local", "bin")
			if kind != "owned" {
				if err := os.Remove(shim); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "foreign-file":
					executable(t, shim, "exit 0")
				case "foreign-symlink":
					link(t, filepath.Join(filepath.Dir(o.Self), "real", "gh"), shim)
				}
			}
			checks := Checks(o)
			ownership := checkNamed(t, checks, "shim-ownership")
			want := StatusWarn
			if kind == "owned" {
				want = StatusOK
			}
			if ownership.Status != want || !strings.Contains(ownership.Detail, shim) || strings.Contains(ownership.Detail, o.BinDir) {
				t.Fatalf("ownership=%+v", ownership)
			}
			precedence := checkNamed(t, checks, "path-precedence")
			want = StatusOK
			if kind == "absent" {
				want = StatusWarn
			}
			if precedence.Status != want || !strings.Contains(precedence.Detail, shim) {
				t.Fatalf("precedence=%+v", precedence)
			}
			if kind == "foreign-symlink" {
				target, err := os.Readlink(shim)
				if err != nil || target != filepath.Join(filepath.Dir(o.Self), "real", "gh") {
					t.Fatalf("doctor changed foreign shim: target=%q err=%v", target, err)
				}
			}
		})
	}
}

func TestDoctorRecordedShimPrecedence(t *testing.T) {
	for _, kind := range []string{"shadowed", "not-on-path", "directory-alias"} {
		t.Run(kind, func(t *testing.T) {
			o := doctorFixture(t)
			bin := o.BinDir
			realDir := filepath.Join(filepath.Dir(o.Self), "real")
			want, detail := StatusWarn, "precedes"
			switch kind {
			case "shadowed":
				o.PathEnv = realDir + ":" + bin
			case "not-on-path":
				o.PathEnv = realDir
				detail = "not on PATH"
			case "directory-alias":
				alias := filepath.Join(filepath.Dir(o.Self), "alias-bin")
				link(t, bin, alias)
				o.PathEnv = alias + ":" + realDir
				want, detail = StatusOK, "first executable gh"
			}
			o.BinDir = ""
			c := checkNamed(t, Checks(o), "path-precedence")
			if c.Status != want || !strings.Contains(c.Detail, detail) || (c.Fix == "") != (want == StatusOK) {
				t.Fatalf("%+v", c)
			}
		})
	}
}

func TestDoctorMultipleRecordedShims(t *testing.T) {
	o := doctorFixture(t)
	first := filepath.Join(o.BinDir, "gh")
	second := filepath.Join(filepath.Dir(o.Self), "other-bin", "gh")
	link(t, o.Self, second)
	if err := saveState(o.StateDir, shimState{second: o.Self, first: o.Self}); err != nil {
		t.Fatal(err)
	}
	o.PathEnv = filepath.Dir(second) + ":" + o.PathEnv
	o.BinDir = ""
	var shimChecks []Check
	for _, c := range Checks(o) {
		if c.Name == "path-precedence" || c.Name == "shim-ownership" {
			shimChecks = append(shimChecks, c)
		}
	}
	if len(shimChecks) != 4 {
		t.Fatalf("shim checks=%+v", shimChecks)
	}
	for i, shim := range []string{first, second} {
		precedence, ownership := shimChecks[2*i], shimChecks[2*i+1]
		want := StatusWarn
		if shim == second {
			want = StatusOK
		}
		if precedence.Name != "path-precedence" || precedence.Status != want || !strings.Contains(precedence.Detail, shim) || ownership.Name != "shim-ownership" || ownership.Status != StatusOK || !strings.HasPrefix(ownership.Detail, shim+" ") {
			t.Fatalf("shim checks out of order or incorrect: %+v", shimChecks)
		}
	}
}

func TestDoctorInvalidShimState(t *testing.T) {
	for _, data := range []string{"invalid", "null"} {
		t.Run(data, func(t *testing.T) {
			o := doctorFixture(t)
			if err := os.WriteFile(filepath.Join(o.StateDir, "shims.json"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			checks := Checks(o)
			precedence := checkNamed(t, checks, "path-precedence")
			ownership := checkNamed(t, checks, "shim-ownership")
			if precedence.Status != StatusFail || ownership.Status != StatusFail || precedence.Detail != ownership.Detail || !strings.Contains(ownership.Detail, "shim state") {
				t.Fatalf("precedence=%+v ownership=%+v", precedence, ownership)
			}
		})
	}
}

func TestDoctorUnrecordedShimFallback(t *testing.T) {
	o := doctorFixture(t)
	if err := os.Remove(filepath.Join(o.StateDir, "shims.json")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"path-precedence", "shim-ownership"} {
		if c := checkNamed(t, Checks(o), name); c.Status != StatusOK || !strings.Contains(c.Detail, filepath.Join(o.BinDir, "gh")) {
			t.Fatalf("%+v", c)
		}
	}
}

func TestDoctorNeverExecutesKnownRenamedShim(t *testing.T) {
	o := doctorFixture(t)
	root := filepath.Dir(o.Self)
	marker := filepath.Join(root, "executed")
	foreignSelf := executable(t, filepath.Join(root, "ghok-old-renamed"), "printf executed > '"+marker+"'")
	otherShim := filepath.Join(root, "other-bin", "gh")
	link(t, foreignSelf, otherShim)
	state, err := loadState(o.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	state[otherShim] = foreignSelf
	if err := saveState(o.StateDir, state); err != nil {
		t.Fatal(err)
	}
	o.PathEnv = filepath.Dir(otherShim) + ":" + o.PathEnv
	checks := Checks(o)
	if c := checkNamed(t, checks, "recursion-risk"); c.Status != StatusFail || !strings.Contains(c.Detail, "recorded shim") {
		t.Errorf("%+v", c)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("known shim executed: %v", err)
	}
}

func TestDoctorVersionTimeoutAndFailure(t *testing.T) {
	o := doctorFixture(t)
	defer func(d time.Duration) { diagnosticTimeout = d }(diagnosticTimeout)
	real := filepath.Join(filepath.Dir(o.Self), "real", "gh")
	for _, tc := range []struct {
		body, detail string
		status       Status
	}{
		{"printf 'gh version 2.39.0 (2023-01-01)\\n'", "predates", StatusWarn},
		{"printf 'gh version 2.102.0 (2026-01-01)\\n'", "2.102.0", StatusInfo},
		{"printf unexpected", "Unrecognized", StatusWarn},
		{"exit 42", "exit status 42", StatusWarn},
		{"exec /bin/sleep 20", "timed out", StatusWarn},
	} {
		t.Run(tc.detail, func(t *testing.T) {
			executable(t, real, tc.body)
			diagnosticTimeout = 10 * time.Second
			if tc.detail == "timed out" {
				diagnosticTimeout = 2 * time.Second // only the sleeping fake needs a short bound
			}
			start := time.Now()
			c := checkNamed(t, Checks(o), "gh-version")
			if c.Status != tc.status || !strings.Contains(c.Detail, tc.detail) {
				t.Errorf("%+v", c)
			}
			if time.Since(start) > 10*time.Second {
				t.Fatal("version diagnostic exceeded generous timeout")
			}
		})
	}
}

func TestDoctorMissingBinaryAndRealGh(t *testing.T) {
	o := doctorFixture(t)
	o.Self = filepath.Join(filepath.Dir(o.Self), "missing")
	checks := Checks(o)
	if c := checkNamed(t, checks, "ghok"); c.Status != StatusFail {
		t.Errorf("%+v", c)
	}
	if c := checkNamed(t, checks, "real-gh"); c.Status != StatusFail {
		t.Errorf("%+v", c)
	}
}

func TestTerminalDetection(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	if !stdinTTY(slave) {
		t.Fatal("PTY not recognized")
	}
	file, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if stdinTTY(file) {
		t.Fatal("regular file recognized as terminal")
	}
}

func TestVersionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		version string
		status  Status
	}{
		{"gh version 1.999.0 (date)\n", StatusWarn},
		{"gh version 2.101.999 (date)\n", StatusWarn},
		{"gh version 2.102.0 (date)\n", StatusInfo},
		{"gh version 2.103.0 (date)\n", StatusInfo},
		{"gh version 3.0.0 (date)\n", StatusInfo},
		{"gh version 999999999999999999999999999.0.0\n", StatusWarn},
	} {
		if got, detail := ghVersionStatus(tc.version); got != tc.status {
			t.Errorf("%q: %s %s", tc.version, got, detail)
		}
	}
}

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
	if _, err := Enable(o); err != nil {
		t.Fatal(err)
	}
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	return DoctorOptions{Self: o.Self, Version: "test", PathEnv: o.PathEnv, BinDir: o.BinDir, StateDir: o.StateDir, Env: []string{"HOME=" + filepath.Dir(o.Self), "XDG_CONFIG_HOME=" + filepath.Join(filepath.Dir(o.Self), "config"), "PATH=/nonexistent"}, Stdin: stdin}
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
	defer master.Close()
	defer slave.Close()
	if !stdinTTY(slave) {
		t.Fatal("PTY not recognized")
	}
	file, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
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

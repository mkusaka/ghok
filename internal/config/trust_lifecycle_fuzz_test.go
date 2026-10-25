package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzTrustLifecycle drives trust, partial trust, untrust, hook/file edits,
// exec-bit flips and file removal against an independent model of what each
// project hook depends on. After every step a project hook is active exactly
// when its current definition and dependency files equal a granted state.
func FuzzTrustLifecycle(f *testing.F) {
	for _, ops := range []string{
		"\x00\x00\x01\x07",                                 // trust all, edit h-a
		"\x00\x01\x01\x08",                                 // trust a, edit h-b
		"\x00\x00\x01\x07\x01\x00",                         // trust, edit h-a, revert h-a
		"\x00\x00\x02\x00\x02\x00",                         // trust, flip exec twice
		"\x00\x00\x03\x03\x04\x06",                         // trust, remove lib-a, list it in a.files
		"\x04\x09\x00\x00\x01\x0d",                         // include shared, trust, edit shared
		"\x00\x00\x05\x00\x00\x02",                         // trust, untrust, trust b
		"\x04\x03\x00\x00\x04\x03",                         // disable a, trust, enable a
		"\x00\x01\x00\x02",                                 // trust a, then b: a stays trusted
		"\x00\x00\x01\x07\x00\x00\x01\x00",                 // trust, edit, trust, revert: history
		"\x00\x00\x04\x00",                                 // trust, change a's timeout
		"\x00\x01\x00\x02\x01\x07",                         // trust a, then b, edit a: a was granted
		"\x00\x00\x01\x07\x00\x00\x01\x0e\x00\x00\x01\x00", // three grants, revert to the first
	} {
		f.Add([]byte(ops))
	}
	// No git on PATH: local configs are never "tracked", and no exec per input.
	f.Setenv("PATH", f.TempDir())
	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) > 80 { // stays below maxGrantHistory grants
			ops = ops[:80]
		}
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		m := newTrustModel(filepath.Join(root, "repo"))
		env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
		writeFixture(t, filepath.Join(m.repo, ".git"), "gitdir: elsewhere\n")
		writeFixture(t, DefaultPath(env), "version: 1\n")
		for _, name := range trustModelFiles {
			m.write(t, name)
		}
		m.writeProject(t)
		m.check(t, env, "initial")
		for i := 0; i+1 < len(ops); i += 2 {
			op, arg := ops[i]%6, ops[i+1]
			name := trustModelFiles[int(arg)%len(trustModelFiles)]
			switch op {
			case 0:
				var ids []string
				for j, id := range trustModelIDs {
					if arg&(1<<j) != 0 {
						ids = append(ids, id)
					}
				}
				if _, _, _, _, err := TrustProject("", m.repo, env, false, true, ids...); err != nil {
					t.Fatalf("trust %v: %v", ids, err)
				}
				for j, id := range trustModelIDs {
					if len(ids) == 0 || arg&(1<<j) != 0 {
						m.granted[m.fingerprint(j)] = true
						m.grantedIDs[id] = true
					}
				}
			case 1:
				m.content[name] = '0' + arg/7%10
				m.exists[name] = true
				m.write(t, name)
			case 2:
				m.exec[name] = !m.exec[name]
				m.write(t, name)
			case 3:
				m.exists[name] = !m.exists[name]
				m.write(t, name)
			case 4:
				j := int(arg) % 3
				switch arg / 3 % 4 {
				case 0:
					m.timeout[j] = m.timeout[j]%3 + 1
				case 1:
					m.disabled[j] = !m.disabled[j]
				case 2:
					m.files[j] = !m.files[j]
				case 3:
					m.include = !m.include
				}
				m.writeProject(t)
			case 5:
				if _, _, err := UntrustProject("", m.repo, env); err != nil {
					t.Fatalf("untrust: %v", err)
				}
				m.granted, m.grantedIDs = map[string]bool{}, map[string]bool{}
			}
			m.check(t, env, fmt.Sprintf("step %d (op %d arg %d)", i/2, op, arg))
		}
	})
}

var (
	trustModelIDs   = []string{"a", "b", "c"}
	trustModelFiles = []string{"h-a", "h-b", "h-c", "lib-a", "lib-b", "lib-c", "shared"}
)

// trustModel is the test's own record of the project; it never asks the
// implementation what a hook depends on.
type trustModel struct {
	repo                string
	timeout             [3]int
	disabled, files     [3]bool
	include             bool
	content             map[string]byte
	exec, exists        map[string]bool
	granted, grantedIDs map[string]bool
}

func newTrustModel(repo string) *trustModel {
	m := &trustModel{repo: repo, timeout: [3]int{1, 1, 1}, content: map[string]byte{}, exec: map[string]bool{}, exists: map[string]bool{}, granted: map[string]bool{}, grantedIDs: map[string]bool{}}
	for _, name := range trustModelFiles {
		m.content[name], m.exists[name], m.exec[name] = '0', true, strings.HasPrefix(name, "h-")
	}
	return m
}

func (m *trustModel) write(t *testing.T, name string) {
	t.Helper()
	path := filepath.Join(m.repo, name)
	if !m.exists[name] {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return
	}
	// Same size on every edit: only content distinguishes versions.
	writeFixture(t, path, string(bytes.Repeat([]byte{m.content[name]}, 8)))
	mode := os.FileMode(0o600)
	if m.exec[name] {
		mode = 0o700
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func (m *trustModel) writeProject(t *testing.T) {
	t.Helper()
	var b strings.Builder
	b.WriteString("version: 1\n")
	if m.include {
		b.WriteString("trust: {include: [shared]}\n")
	}
	b.WriteString("hooks:\n  pre:\n")
	for j, id := range trustModelIDs {
		fmt.Fprintf(&b, "  - {id: %s, command: [./h-%s], timeout: %ds, enabled: %t", id, id, m.timeout[j], !m.disabled[j])
		if m.files[j] {
			fmt.Fprintf(&b, ", files: [lib-%s]", id)
		}
		b.WriteString("}\n")
	}
	writeFixture(t, filepath.Join(m.repo, ".ghok.yaml"), b.String())
}

func (m *trustModel) fingerprint(j int) string {
	id := trustModelIDs[j]
	deps := []string{"h-" + id}
	if m.files[j] {
		deps = append(deps, "lib-"+id)
	}
	if m.include {
		deps = append(deps, "shared")
	}
	s := fmt.Sprintf("%s timeout=%d disabled=%t", id, m.timeout[j], m.disabled[j])
	for _, name := range deps {
		if m.exists[name] {
			s += fmt.Sprintf(" %s=%c/%t", name, m.content[name], m.exec[name])
		} else {
			s += " " + name + "=missing"
		}
	}
	return s
}

func (m *trustModel) check(t *testing.T, env map[string]string, step string) {
	t.Helper()
	c, _, err := LoadEffective("", m.repo, env)
	if err != nil {
		t.Fatalf("%s: %v", step, err)
	}
	project := filepath.Join(m.repo, ".ghok.yaml")
	active, reasons := map[string]bool{}, map[string]string{}
	for _, h := range c.Hooks {
		if h.Source == project {
			active[h.ID] = true
		}
	}
	for _, h := range c.IgnoredHooks {
		reasons[h.ID] = h.Reason
	}
	for j, id := range trustModelIDs {
		fp := m.fingerprint(j)
		switch {
		case active[id] && !m.granted[fp]:
			t.Fatalf("%s: hook %s active but never granted in state %q", step, id, fp)
		case !active[id] && m.granted[fp]:
			t.Fatalf("%s: hook %s granted in state %q but inactive (%s)", step, id, fp, reasons[id])
		case !active[id] && m.grantedIDs[id] && reasons[id] != "changed since trust":
			t.Fatalf("%s: changed hook %s reported %q", step, id, reasons[id])
		case !active[id] && !m.grantedIDs[id] && reasons[id] != "untrusted":
			t.Fatalf("%s: never-granted hook %s reported %q", step, id, reasons[id])
		}
	}
}

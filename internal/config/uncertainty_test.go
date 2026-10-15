package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUncertaintyConfigurationValidation(t *testing.T) {
	for _, value := range []string{"allow", "audit", "deny"} {
		for _, unresolved := range []string{"deny", "ignore"} {
			body := "version: 1\npolicy:\n  unknownInput: " + value + "\n  rules:\n    - id: check\n      decision: deny\n      onUnresolved: " + unresolved + "\n"
			if _, err := Parse([]byte(body), t.TempDir()); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct{ body, field string }{
		{"policy: {unknownInput: invalid}", "policy.unknownInput"},
		{"policy: {rules: [{id: check, decision: deny, onUnresolved: allow}]}", "policy.rules[0].onUnresolved"},
	} {
		_, err := Parse([]byte("version: 1\n"+tc.body), t.TempDir())
		if err == nil || !strings.Contains(err.Error(), tc.field) {
			t.Fatalf("validation error: %v", err)
		}
	}
}

func TestProjectUnknownInputOnlyTightens(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(home, "repo")
	global := filepath.Join(root, "config", "config.yaml")
	project := filepath.Join(repo, ".ghok.yaml")
	env := map[string]string{"HOME": home, "GHOK_CONFIG_DIR": filepath.Dir(global)}
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	for _, tc := range []struct {
		global, project, want string
		weakening             bool
	}{
		{"deny", "allow", "deny", true},
		{"allow", "deny", "deny", false},
		{"allow", "", "allow", false},
	} {
		writeFixture(t, global, "version: 1\npolicy: {unknownInput: "+tc.global+"}\n")
		body := "version: 1\n"
		if tc.project != "" {
			body += "policy: {unknownInput: " + tc.project + "}\n"
		}
		writeFixture(t, project, body)
		c, notes, err := LoadEffective(global, repo, env)
		if err != nil || c.Policy.UnknownInput != tc.want {
			t.Fatalf("effective policy: %+v, %v", c, err)
		}
		if strings.Contains(strings.Join(notes, "\n"), "unknownInput would weaken") != tc.weakening {
			t.Fatalf("notes: %v", notes)
		}
	}
}

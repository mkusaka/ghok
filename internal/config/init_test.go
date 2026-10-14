package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestInitTemplates(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "GHOK_CONFIG_DIR": filepath.Join(home, "cfg")}

	user, err := Init(ScopeUser, repo, env)
	if err != nil || user != filepath.Join(home, "cfg", "config.yaml") {
		t.Fatalf("user init = %q, %v", user, err)
	}
	c, err := Load(user)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("user template invalid: %v", err)
	}

	project, err := Init(ScopeProject, filepath.Join(repo), env)
	if err != nil || filepath.Base(project) != ".ghok.yaml" {
		t.Fatalf("project init = %q, %v", project, err)
	}
	if strings.Contains(mustRead(t, project), home) {
		t.Fatal("shared project template must not embed the creator's paths")
	}
	// Trusting the generated project file must load cleanly on top of the user config.
	if _, _, notes, err := TrustProject(project, repo, env); err != nil {
		t.Fatalf("trust project template: %v (notes %v)", err, notes)
	}
	if _, notes, err := LoadEffective(user, repo, env); err != nil {
		t.Fatalf("trusted project template: %v (notes %v)", err, notes)
	}

	for scope, path := range map[string]string{ScopeUser: user, ScopeProject: project} {
		var doc any
		if err := yaml.Unmarshal([]byte(mustRead(t, path)), &doc); err != nil {
			t.Fatal(err)
		}
		if err := checkSchema(Schema(scope == ScopeProject), doc, "$"); err != nil {
			t.Fatalf("%s template vs schema: %v", scope, err)
		}
		if _, err := Init(scope, repo, env); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("%s: second init must refuse to overwrite, got %v", scope, err)
		}
	}
	if _, err := Init(ScopeProject, home, env); err == nil {
		t.Fatal("project init outside a git repository must fail")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

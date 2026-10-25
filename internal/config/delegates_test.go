package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDelegateConfig(t *testing.T) {
	valid := "version: 1\ndelegates: [{id: relay, match: {effect: read, level: L1, operation: pr.view}, command: [relay]}]\n"
	c, err := Parse([]byte(valid), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Delegates) != 1 || c.Delegates[0].Timeout.D() != DefaultDelegateTimeout || c.Delegates[0].OnError != OnErrorFallback {
		t.Fatalf("defaults: %+v", c.Delegates)
	}
	for _, match := range []string{
		"{}", "{operation: pr.merge.*}", "{effect: write, level: L1}", "{effect: [read, write], level: L1}",
		"{effect: read}", "{effect: read, level: L3}", "{effect: read, level: [L1, L3]}",
		"{effect: read, level: L2}", "{effect: read, level: L2, method: POST}",
		"{effect: read, level: L2, method: [GET, POST]}",
	} {
		data := "version: 1\ndelegates: [{id: unsafe, match: " + match + ", command: [./relay]}]\n"
		if _, err := Parse([]byte(data), ""); err == nil {
			t.Fatalf("accepted unsafe user match %s", match)
		}
		if _, _, err := parseProject([]byte(data), "/repo/.ghok.yaml"); err == nil {
			t.Fatalf("accepted unsafe project match %s", match)
		}
	}
	for _, field := range []string{"timeout: 0s", "timeout: 11m", "onError: allow", "unless: [{}]", "unless: [{exitCode: [8]}]", "files: []", "env: {bad-name: value}", "unexpected: true"} {
		data := strings.Replace(valid, "command: [relay]", "command: [relay], "+field, 1)
		if _, err := Parse([]byte(data), ""); err == nil {
			t.Fatalf("accepted %s", field)
		}
	}
	for _, extra := range []string{
		"hooks: {pre: [{id: relay, command: [true]}]}",
		"transforms: {pre: [{id: relay, command: [true]}]}",
	} {
		if _, err := Parse([]byte(valid+extra+"\n"), ""); err == nil {
			t.Fatalf("accepted duplicate ID: %s", extra)
		}
	}
	api := strings.Replace(valid, "level: L1, operation: pr.view", "level: L2, command: api, method: GET", 1)
	if _, err := Parse([]byte(api), ""); err != nil {
		t.Fatal(err)
	}
}

func TestDelegateTrustAndLayers(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	user := "version: 1\ndelegates: [{id: user, match: {effect: read, level: L1}, command: [/bin/true]}]\n"
	project := "version: 1\ndelegates: [{id: project, match: {effect: read, level: L1}, command: [./hook], files: [script]}]\n"
	writeFixture(t, DefaultPath(env), user)
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), project)
	writeFixture(t, filepath.Join(repo, "script"), "first")
	load := func(active, ignored int) *Config {
		t.Helper()
		c, _, err := LoadEffective("", repo, env)
		if err != nil || len(c.Delegates) != active || len(c.IgnoredHooks) != ignored {
			t.Fatalf("active=%d ignored=%d config=%+v err=%v", active, ignored, c, err)
		}
		for _, h := range c.IgnoredHooks {
			if !h.Delegate || h.GateBlocked {
				t.Fatalf("delegate became inactive gate: %+v", h)
			}
		}
		return c
	}
	load(1, 1)
	if _, _, _, _, err := TrustProject("", repo, env, false, true, "project"); err != nil {
		t.Fatal(err)
	}
	c := load(2, 0)
	if c.Delegates[0].ID != "user" || c.Delegates[1].ID != "project" || !filepath.IsAbs(c.Delegates[1].Command[0]) {
		t.Fatalf("ordering/path: %+v", c.Delegates)
	}
	writeFixture(t, filepath.Join(repo, "script"), "changed")
	load(1, 1)
	writeFixture(t, DefaultPath(env), user+"trust: {repositories: [\""+repo+"\"]}\n")
	load(2, 0)
	writeFixture(t, filepath.Join(repo, LocalName), "version: 1\ndelegates: [{id: local, match: {effect: read, level: L1}, command: [./hook]}]\n")
	c = load(3, 0)
	if c.Delegates[2].ID != "local" {
		t.Fatalf("local layer: %+v", c.Delegates)
	}
}

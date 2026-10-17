package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTransformConfig(t *testing.T) {
	c, err := Parse([]byte("version: 1\ntransforms: {pre: [{id: edit, command: [filter]}]}\n"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Transforms) != 1 || len(c.Hooks) != 0 || c.Transforms[0].Timeout.D() != DefaultTransformTimeout || c.Transforms[0].OnError != OnErrorDeny {
		t.Fatalf("defaults: %+v", c)
	}
	for _, post := range []string{"null", "[]", "{}", "false", "reserved"} {
		_, err := Parse([]byte("version: 1\ntransforms: {post: "+post+"}\n"), "")
		if err == nil || !strings.Contains(err.Error(), "transforms.post is reserved") {
			t.Fatalf("post %s: %v", post, err)
		}
	}
	for _, key := range []string{"parallel", "background", "wait", "wait-all", "dependsOn", "optional", "captureOutput", "enabled", "maxOutputBytes", "phase"} {
		data := []byte("version: 1\ntransforms: {pre: [{id: edit, command: [./filter], " + key + ": null}]}\n")
		if _, err := Parse(data, ""); err == nil {
			t.Fatalf("accepted user %s", key)
		}
		if _, _, err := parseProject(data, "/repo/.ghok.yaml"); err == nil {
			t.Fatalf("accepted project %s", key)
		}
	}
	for _, suffix := range []string{
		"hooks: {pre: [{id: edit, command: [filter]}]}",
		"transforms: {pre: [{id: edit, command: [filter]}, {id: edit, command: [filter]}]}",
	} {
		data := "version: 1\n" + suffix + "\n"
		if strings.HasPrefix(suffix, "hooks") {
			data += "transforms: {pre: [{id: edit, command: [filter]}]}\n"
		}
		if _, err := Parse([]byte(data), ""); err == nil {
			t.Fatalf("accepted collision: %s", data)
		}
	}
	for _, field := range []string{"timeout: 0s", "onError: ignore", "match: {result: success}", "files: []"} {
		if _, err := Parse([]byte("version: 1\ntransforms: {pre: [{id: edit, command: [filter], "+field+"}]}\n"), ""); err == nil {
			t.Fatalf("accepted %s", field)
		}
	}
}

func TestTransformRejectsContinueOnError(t *testing.T) {
	for _, value := range []string{"true", "false", "null"} {
		data := []byte("version: 1\ntransforms: {pre: [{id: edit, command: [./filter], continueOnError: " + value + "}]}\n")
		_, userErr := Parse(data, "")
		_, _, projectErr := parseProject(data, "/repo/.ghok.yaml")
		for _, err := range []error{userErr, projectErr} {
			if err == nil || !strings.Contains(err.Error(), "transforms.pre[0].continueOnError") || !strings.Contains(err.Error(), "use onError: allow") {
				t.Fatalf("continueOnError %s: %v", value, err)
			}
		}
	}
}

func TestTransformTimeoutRange(t *testing.T) {
	for _, timeout := range []string{"15s", MaxHookTimeout.String()} {
		c, err := Parse([]byte("version: 1\ntransforms: {pre: [{id: edit, command: [filter], timeout: "+timeout+"}]}\n"), "")
		if err != nil {
			t.Fatalf("timeout %s rejected: %v", timeout, err)
		}
		if c.Transforms[0].Timeout.D().String() != timeout {
			t.Fatalf("timeout %s was not retained", timeout)
		}
	}
	timeout := (MaxHookTimeout + 1).String()
	if _, err := Parse([]byte("version: 1\ntransforms: {pre: [{id: edit, command: [filter], timeout: "+timeout+"}]}\n"), ""); err == nil {
		t.Fatal("timeout above the hook maximum accepted")
	}
}

func TestTransformSelectiveTrustAndNoGate(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	body := "version: 1\nhooks: {pre: [{id: gate, command: [./hook]}]}\ntransforms: {pre: [{id: edit, command: [./hook], files: [own]}]}\ntrust: {include: [shared]}\n"
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), body)
	writeFixture(t, filepath.Join(repo, "own"), "first")
	writeFixture(t, filepath.Join(repo, "shared"), "shared")
	if _, _, _, _, err := TrustProject("", repo, env, false, true, "edit"); err != nil {
		t.Fatal(err)
	}
	c, _, err := LoadEffective("", repo, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Transforms) != 1 || len(c.Hooks) != 0 || len(c.IgnoredHooks) != 1 || c.IgnoredHooks[0].GateBlocked {
		t.Fatalf("transform activated hook gate: %+v", c)
	}
	if _, _, _, _, err := TrustProject("", repo, env, false, true, "gate"); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(repo, "own"), "changed")
	c, _, err = LoadEffective("", repo, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Transforms) != 0 || len(c.Hooks) != 1 || len(c.IgnoredHooks) != 1 || !c.IgnoredHooks[0].Transform || c.IgnoredHooks[0].GateBlocked {
		t.Fatalf("changed transform became gate: %+v", c)
	}
	if _, _, _, _, err := TrustProject("", repo, env, false, true, "edit"); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(repo, "shared"), "changed shared")
	c, _, err = LoadEffective("", repo, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Transforms) != 0 || len(c.Hooks) != 0 || len(c.IgnoredHooks) != 2 {
		t.Fatalf("shared trust dependency ignored: %+v", c)
	}
}

func TestTransformUserProjectMerge(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	writeFixture(t, DefaultPath(env), "version: 1\ntransforms: {pre: [{id: user, command: [/bin/true]}]}\n")
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\ntransforms: {pre: [{id: project, command: [./hook]}]}\n")
	if _, _, _, _, err := TrustProject("", repo, env, false, true); err != nil {
		t.Fatal(err)
	}
	c, _, err := LoadEffective("", repo, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Transforms) != 2 || c.Transforms[0].ID != "user" || c.Transforms[1].ID != "project" || !filepath.IsAbs(c.Transforms[1].Command[0]) {
		t.Fatalf("merge: %+v", c.Transforms)
	}
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks: {pre: [{id: user, command: [./hook]}]}\n")
	c, _, err = LoadEffective("", repo, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Transforms) != 1 || len(c.Hooks) != 0 || len(c.Diagnostics) == 0 {
		t.Fatalf("collision discarded user config: %+v", c)
	}
}

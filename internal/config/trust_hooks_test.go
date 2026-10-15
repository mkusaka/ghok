package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func hookTrustRecord(t *testing.T, repo string, env map[string]string) trustEntry {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(filepath.Join(repo, ".ghok.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	store, notes := readTrustStore(env)
	entry, ok := store[contentHash([]byte(canonical))]
	if !ok || entry.ManifestVersion != 2 {
		t.Fatalf("missing v2 checkout record: %+v, %v", store, notes)
	}
	return entry
}

func assertHookTrust(t *testing.T, repo string, env map[string]string, active, ignored []string) *Config {
	t.Helper()
	c, notes, err := LoadEffective("", repo, env)
	if err != nil {
		t.Fatal(err)
	}
	var gotActive, gotIgnored []string
	for _, hook := range c.Hooks {
		gotActive = append(gotActive, hook.ID)
	}
	for _, hook := range c.IgnoredHooks {
		gotIgnored = append(gotIgnored, hook.ID)
	}
	if !reflect.DeepEqual(gotActive, active) || !reflect.DeepEqual(gotIgnored, ignored) {
		t.Fatalf("active=%v ignored=%v; want active=%v ignored=%v; notes=%v", gotActive, gotIgnored, active, ignored, notes)
	}
	return c
}

func TestHookTrustIgnoresNonExecutableYAMLChanges(t *testing.T) {
	for _, replacement := range []string{
		"# new comment\nversion: 1\nhooks:\n  pre:\n    - id: review\n      command:\n        - ./hook\npolicy:\n  mode: enforce\n",
		"hooks: {pre: [{command: [./hook], id: review}]}\npolicy: {mode: enforce}\nversion: 1\n",
		"version: 1\npolicy: {mode: observe, unknownMutation: deny, rules: [{id: block, decision: deny}]}\nhooks: {pre: [{id: review, command: [./hook]}]}\n",
	} {
		t.Run(replacement, func(t *testing.T) {
			repo, env, _ := trustRepositoryFixture(t)
			if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
				t.Fatal(err)
			}
			before := hookTrustRecord(t, repo, env)
			writeFixture(t, filepath.Join(repo, ".ghok.yaml"), replacement)
			assertHookTrust(t, repo, env, []string{"review"}, nil)
			if _, _, _, diff, err := TrustProject("", repo, env, false, false); err != nil || len(diff) != 0 {
				t.Fatalf("non-executable YAML generated file diff: %q, %v", diff, err)
			}
			after := hookTrustRecord(t, repo, env)
			if !reflect.DeepEqual(before.HookDigests, after.HookDigests) || !reflect.DeepEqual(before.Definitions, after.Definitions) {
				t.Fatalf("non-executable YAML changed canonical hook state: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestHookTrustDependencyIsolation(t *testing.T) {
	const body = "version: 1\nhooks:\n  pre:\n  - {id: a, command: [./a], files: [own-a, shared]}\n  - {id: b, command: [./b], files: [own-b, shared]}\n  - {id: c, command: [./c]}\ntrust: {include: [global]}\n"
	for _, tc := range []struct {
		name            string
		active, ignored []string
	}{
		{"own-a", []string{"b", "c"}, []string{"a"}},
		{"a", []string{"b", "c"}, []string{"a"}},
		{"shared", []string{"c"}, []string{"a", "b"}},
		{"global", nil, []string{"a", "b", "c"}},
		{"definition", []string{"b", "c"}, []string{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, env, _ := trustRepositoryFixture(t)
			project := filepath.Join(repo, ".ghok.yaml")
			writeFixture(t, project, body)
			for _, path := range []string{"a", "b", "c", "own-a", "own-b", "shared", "global"} {
				writeFixture(t, filepath.Join(repo, path), "reviewed "+path)
			}
			if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
				t.Fatal(err)
			}
			if tc.name == "definition" {
				writeFixture(t, project, strings.Replace(body, "command: [./a]", "command: [./a, changed]", 1))
			} else {
				writeFixture(t, filepath.Join(repo, tc.name), "changed")
			}
			c := assertHookTrust(t, repo, env, tc.active, tc.ignored)
			for _, hook := range c.IgnoredHooks {
				if hook.Reason != "changed since trust" || !validDigest(hook.Digest) {
					t.Fatalf("changed hook missing trust details: %+v", hook)
				}
				if tc.name != "definition" && !reflect.DeepEqual(hook.ChangedFiles, []string{tc.name}) {
					t.Fatalf("wrong hook dependency changes: %+v", hook)
				}
			}
			if tc.name == "definition" {
				_, notes, err := LoadEffective("", repo, env)
				if err != nil || !strings.Contains(strings.ToLower(strings.Join(notes, "\n")), "definition changed") {
					t.Fatalf("definition change not explained: %v, %v", notes, err)
				}
			}
		})
	}
}

func TestHookTrustDefinitionFields(t *testing.T) {
	for _, tc := range []struct{ name, base, change string }{
		{"id", "id: a", "id: renamed"},
		{"phase", "pre", "post"},
		{"command", "command: [./hook]", "command: [./hook, arg]"},
		{"env", "", "env: {SETTING: changed}"},
		{"timeout", "", "timeout: 3s"},
		{"onError", "", "onError: allow"},
		{"optional", "", "optional: true"},
		{"enabled", "", "enabled: false"},
		{"redactInput", "", "redactInput: true"},
		{"maxOutputBytes", "", "maxOutputBytes: 1024"},
		{"files", "", "files: [extra]"},
		{"operation", "", "match: {operation: pr.merge.*}"},
		{"command match", "", "match: {command: pr merge}"},
		{"repositories", "", "match: {repositories: org/repo}"},
		{"hosts", "", "match: {hosts: github.com}"},
		{"effect", "", "match: {effect: write}"},
		{"level", "", "match: {level: L1}"},
		{"token", "", "match: {token: app}"},
		{"match env", "", "match: {env: {CI: '*'}}"},
		{"captureOutput", "post", "captureOutput: true"},
		{"result", "post", "match: {result: success}"},
		{"exitCode", "post", "match: {exitCode: [1]}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, env, _ := trustRepositoryFixture(t)
			project := filepath.Join(repo, ".ghok.yaml")
			hook := "id: a, command: [./hook]"
			phase := "pre"
			if tc.base == "post" {
				phase = "post"
			}
			body := func() string {
				if phase == "post" {
					return "version: 1\nhooks: {pre: [{id: sibling, command: [./hook]}], post: [{" + hook + "}]}\n"
				}
				return "version: 1\nhooks: {pre: [{" + hook + "}, {id: sibling, command: [./hook]}]}\n"
			}
			writeFixture(t, project, body())
			if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
				t.Fatal(err)
			}
			before := hookTrustRecord(t, repo, env)
			if tc.name == "phase" {
				phase = tc.change
			} else if tc.base != "" && tc.base != "post" {
				hook = strings.Replace(hook, tc.base, tc.change, 1)
			} else {
				hook += ", " + tc.change
			}
			writeFixture(t, project, body())
			if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
				t.Fatal(err)
			}
			after := hookTrustRecord(t, repo, env)
			id := "a"
			if tc.name == "id" {
				id = "renamed"
			}
			if after.HookDigests[id] == before.HookDigests["a"] || after.HookDigests["sibling"] != before.HookDigests["sibling"] {
				t.Fatalf("definition field failed to isolate digest change: before=%+v after=%+v", before.HookDigests, after.HookDigests)
			}
		})
	}
}

func TestHookTrustFileDeduplication(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks:\n  pre:\n  - {id: a, command: [./hook], files: [hook, ./hook, data, data]}\n  - {id: b, command: [./hook], files: [data]}\ntrust: {include: [hook, data, ./data]}\n")
	writeFixture(t, filepath.Join(repo, "data"), "shared data")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	entry := hookTrustRecord(t, repo, env)
	if len(entry.Files) != 2 {
		t.Fatalf("shared dependencies not deduplicated: %+v", entry.Files)
	}
	for _, file := range entry.Files {
		if !reflect.DeepEqual(file.HookIDs, []string{"a", "b"}) {
			t.Fatalf("shared dependency omitted users: %+v", file)
		}
	}
}

func TestSelectiveHookTrustPreservesSiblingBaseline(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks: {pre: [{id: a, command: [./hook]}, {id: b, command: [./hook]}]}\n")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	before := hookTrustRecord(t, repo, env)
	writeFixture(t, filepath.Join(repo, "hook"), "selectively reviewed code\n")
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks: {pre: [{id: a, command: [./hook]}, {id: b, command: [./hook], timeout: 3s}]}\n")
	if _, _, _, _, err := TrustProject("", repo, env, false, false, "a"); err != nil {
		t.Fatal(err)
	}
	after := hookTrustRecord(t, repo, env)
	if after.HookDigests["a"] == before.HookDigests["a"] || after.HookDigests["b"] != before.HookDigests["b"] || !reflect.DeepEqual(after.Definitions["b"], before.Definitions["b"]) {
		t.Fatalf("selective trust overwrote sibling state: before=%+v after=%+v", before, after)
	}
	assertHookTrust(t, repo, env, []string{"a"}, []string{"b"})
	if data, err := readTrustBlob(env, contentHash([]byte("reviewed code\n"))); err != nil || string(data) != "reviewed code\n" {
		t.Fatalf("selective trust collected sibling baseline: %q, %v", data, err)
	}
	writeFixture(t, filepath.Join(repo, "hook"), "latest code\n")
	for _, tc := range []struct{ id, baseline string }{{"a", "selectively reviewed code"}, {"b", "reviewed code"}} {
		_, _, _, diff, err := TrustProject("", repo, env, true, false, tc.id)
		if err != nil || !strings.Contains(string(diff), "-"+tc.baseline+"\n+latest code\n") {
			t.Fatalf("%s lost exact trusted baseline: %s, %v", tc.id, diff, err)
		}
	}
}

func TestProjectHookFilesValidation(t *testing.T) {
	for _, path := range []string{"", "/absolute", "../outside", "a/../b", "*.go", "dir", "link", "dangling/child"} {
		t.Run(path, func(t *testing.T) {
			repo, env, _ := trustRepositoryFixture(t)
			if err := os.Mkdir(filepath.Join(repo, "dir"), 0700); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(filepath.Dir(repo), "outside")
			writeFixture(t, outside, "outside")
			for _, name := range []string{"link", "dangling"} {
				target := outside
				if name == "dangling" {
					target += "-missing"
				}
				if err := os.Symlink(target, filepath.Join(repo, name)); err != nil {
					t.Fatal(err)
				}
			}
			quoted, err := json.Marshal(path)
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks: {pre: [{id: review, command: [./hook], files: ["+string(quoted)+"]}]}\n")
			if _, _, _, _, err := TrustProject("", repo, env, false, false); err == nil {
				t.Fatalf("unsafe dependency %q accepted", path)
			}
			c, _, err := LoadEffective("", repo, env)
			if err != nil || len(c.Hooks) != 0 {
				t.Fatalf("unsafe project must not activate hooks: %+v, %v", c, err)
			}
		})
	}
}

func TestMissingProjectHookFilesTracked(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks: {pre: [{id: a, command: [./hook], files: [missing]}, {id: b, command: [./hook]}]}\n")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	assertHookTrust(t, repo, env, []string{"a", "b"}, nil)
	writeFixture(t, filepath.Join(repo, "missing"), "appeared")
	assertHookTrust(t, repo, env, []string{"b"}, []string{"a"})
}

func TestUserHookFilesRejected(t *testing.T) {
	for _, files := range []string{"[data]", "[]"} {
		body := []byte("version: 1\nhooks: {pre: [{id: first, command: [hook]}, {id: second, command: [hook], files: " + files + "}]}\n")
		if _, err := parse(body, "", true); err == nil || !strings.Contains(err.Error(), "hook second: files:") {
			t.Fatalf("user files must fail for hook second: %v", err)
		}
	}
}

func TestLegacyTrustRecordsNeverAuthorize(t *testing.T) {
	for _, version := range []int{0, 1} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			repo, env, _ := trustRepositoryFixture(t)
			if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
				t.Fatal(err)
			}
			entry := hookTrustRecord(t, repo, env)
			entry.ManifestVersion = version
			data, err := json.Marshal(map[string]trustEntry{contentHash([]byte(entry.FirstPath)): entry})
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, trustStorePath(env), string(data))
			c := assertHookTrust(t, repo, env, nil, []string{"review"})
			if c.IgnoredHooks[0].Reason != "untrusted" || c.IgnoredHooks[0].GateBlocked {
				t.Fatalf("legacy grant treated as trusted history: %+v", c.IgnoredHooks)
			}
		})
	}
}

func TestHookTrustStableAcrossUnrelatedHookAndMapOrder(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	project := filepath.Join(repo, ".ghok.yaml")
	writeFixture(t, project, "version: 1\nhooks:\n  pre:\n  - {id: a, command: [./hook], env: {A: one, B: two}, files: [x, y]}\n  - {id: b, command: [./hook]}\n")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	before := hookTrustRecord(t, repo, env)
	writeFixture(t, project, "version: 1\nhooks:\n  pre:\n  - {id: b, command: [./hook]}\n  - {id: a, command: [./hook], env: {B: two, A: one}, files: [x, y]}\n")
	assertHookTrust(t, repo, env, []string{"b", "a"}, nil)
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	after := hookTrustRecord(t, repo, env)
	if !reflect.DeepEqual(before.HookDigests, after.HookDigests) {
		t.Fatalf("unrelated hook or map ordering changed hook digests: before=%v after=%v", before.HookDigests, after.HookDigests)
	}
}

func TestSelectiveInitialTrustLeavesSiblingUntrusted(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks: {pre: [{id: a, command: [./hook]}, {id: b, command: [./hook]}]}\n")
	if _, _, _, _, err := TrustProject("", repo, env, false, false, "a"); err != nil {
		t.Fatal(err)
	}
	entry := hookTrustRecord(t, repo, env)
	if len(entry.HookDigests) != 1 || !validDigest(entry.HookDigests["a"]) {
		t.Fatalf("selective trust granted unselected sibling: %+v", entry)
	}
	c := assertHookTrust(t, repo, env, []string{"a"}, []string{"b"})
	if c.IgnoredHooks[0].Reason != "untrusted" || !c.IgnoredHooks[0].GateBlocked {
		t.Fatalf("missing deny gate beside active hook must be blocked: %+v", c.IgnoredHooks)
	}
}

func TestHookFilesContainedSymlinkAndRemoval(t *testing.T) {
	repo, env, _ := trustRepositoryFixture(t)
	writeFixture(t, filepath.Join(repo, "data"), "reviewed data")
	if err := os.Symlink("data", filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks: {pre: [{id: a, command: [./hook], files: [link]}, {id: b, command: [./hook]}]}\n")
	if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
		t.Fatal(err)
	}
	assertHookTrust(t, repo, env, []string{"a", "b"}, nil)
	if err := os.Remove(filepath.Join(repo, "data")); err != nil {
		t.Fatal(err)
	}
	assertHookTrust(t, repo, env, []string{"b"}, []string{"a"})
}

func TestIgnoredHookGateBlocking(t *testing.T) {
	for _, tc := range []struct {
		name, phase, definition string
		trust, blocked          bool
	}{
		{"never trusted", "pre", "", false, false},
		{"default deny", "pre", "", true, true},
		{"explicit deny", "pre", ", onError: deny", true, true},
		{"allow", "pre", ", onError: allow", true, false},
		{"ignore", "pre", ", onError: ignore", true, false},
		{"disabled", "pre", ", enabled: false", true, false},
		{"post", "post", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, env, _ := trustRepositoryFixture(t)
			writeFixture(t, filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks: {"+tc.phase+": [{id: review, command: [./hook]"+tc.definition+"}]}\n")
			if tc.trust {
				if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
					t.Fatal(err)
				}
			}
			writeFixture(t, filepath.Join(repo, "hook"), "changed\n")
			c := assertHookTrust(t, repo, env, nil, []string{"review"})
			if c.IgnoredHooks[0].GateBlocked != tc.blocked {
				t.Fatalf("gate blocking=%v, want %v: %+v", c.IgnoredHooks[0].GateBlocked, tc.blocked, c.IgnoredHooks[0])
			}
			if tc.blocked {
				_, notes, err := LoadEffective("", repo, env)
				if err != nil || !strings.Contains(strings.Join(notes, "\n"), "gate hook review inactive: changed since trust") {
					t.Fatalf("inactive gate not identified: %v, %v", notes, err)
				}
			}
		})
	}
}

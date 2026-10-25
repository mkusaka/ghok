//go:build unix

package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/policy"
	"github.com/mkusaka/ghok/internal/protocol"
)

func script(t testing.TB, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func hook(id, path string) config.Hook {
	return config.Hook{ID: id, Phase: "pre", Command: []string{path}, Timeout: config.Duration(30 * time.Second)} // generous: hooks are slow to start under parallel test load
}
func input() protocol.Input {
	return protocol.Input{SchemaVersion: 1, Event: "pre", EventID: "test-event", Operations: []model.Operation{{Family: "pr.merge", Action: "pr.merge.direct", Effect: model.EffectWrite, Execution: model.ExecutionWillRun}}, Invocation: protocol.InvocationInfo{Argv: []string{"pr", "merge", "1"}}}
}
func hermetic(t testing.TB) []string {
	t.Helper()
	home := t.TempDir()
	return []string{"PATH=/usr/bin:/bin", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, "config")}
}
func one(t *testing.T, h config.Hook, r *Runner) Aggregate {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.Run(ctx, h.Phase, []config.Hook{h}, input())
}

func TestResponseOutcomes(t *testing.T) {
	for _, c := range []struct {
		name, body string
		outcome    protocol.Outcome
		decision   protocol.Decision
		code       int
	}{
		{"allow", `printf '{"schemaVersion":1,"decision":"allow"}'`, protocol.OutcomeOK, protocol.DecisionAllow, 0},
		{"deny", `printf '{"decision":"deny","reason":"policy reason","message":"user message"}'`, protocol.OutcomeOK, protocol.DecisionDeny, 0},
		{"abstain", `printf '{"decision":"abstain"}'`, protocol.OutcomeOK, protocol.DecisionAbstain, 0},
		{"empty", "exit 0", protocol.OutcomeOK, protocol.DecisionAbstain, 0},
		{"whitespace", `printf ' \n '`, protocol.OutcomeOK, protocol.DecisionAbstain, 0},
		{"bad JSON", "printf 'not JSON'", protocol.OutcomeInvalidResponse, "", 0},
		{"trailing garbage", `printf '{"decision":"allow"}garbage'`, protocol.OutcomeInvalidResponse, "", 0},
		{"second document", `printf '{"decision":"allow"}{"decision":"deny"}'`, protocol.OutcomeInvalidResponse, "", 0},
		{"unknown decision", `printf '{"decision":"audit"}'`, protocol.OutcomeInvalidResponse, "", 0},
		{"no decision", `printf '{}'`, protocol.OutcomeInvalidResponse, "", 0},
		{"null", `printf 'null'`, protocol.OutcomeInvalidResponse, "", 0},
		{"schema zero", `printf '{"schemaVersion":0,"decision":"allow"}'`, protocol.OutcomeOK, protocol.DecisionAllow, 0},
		{"schema future", `printf '{"schemaVersion":2,"decision":"allow"}'`, protocol.OutcomeInvalidResponse, "", 0},
		{"nonzero beats allow", `printf '{"decision":"allow"}'; exit 7`, protocol.OutcomeError, "", 7},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := hook("test", script(t, c.body))
			r := &Runner{Env: hermetic(t)}
			got := one(t, h, r)
			v := got.Results[0]
			if v.Outcome != c.outcome || v.Decision != c.decision || v.ExitCode != c.code || v.ContinuedOnError {
				t.Fatalf("%+v", v)
			}
			if c.name == "deny" && (got.Message != "user message" || v.Reason != "policy reason") {
				t.Fatalf("%+v", got)
			}
			if c.outcome != protocol.OutcomeOK && (v.Error == "" || got.Decision != protocol.DecisionDeny) {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestOnErrorAndPost(t *testing.T) {
	path := script(t, "exit 3")
	for _, c := range []struct {
		phase, onError string
		want           protocol.Decision
	}{
		{"pre", "", protocol.DecisionDeny}, {"pre", "deny", protocol.DecisionDeny},
		{"pre", "allow", protocol.DecisionAllow}, {"pre", "ignore", protocol.DecisionAbstain},
		{"post", "", protocol.DecisionAbstain}, {"post", "deny", protocol.DecisionAbstain},
		{"post", "allow", protocol.DecisionAbstain}, {"post", "ignore", protocol.DecisionAbstain},
	} {
		h := hook("test", path)
		h.Phase = c.phase
		h.OnError = c.onError
		got := one(t, h, &Runner{Env: hermetic(t)})
		if got.Decision != c.want || got.Results[0].Effective != c.want {
			t.Fatalf("%s %s: %+v", c.phase, c.onError, got)
		}
	}
	h := hook("post-deny", script(t, `printf '{"decision":"deny","message":"no"}'`))
	h.Phase = "post"
	got := one(t, h, &Runner{Env: hermetic(t)})
	if got.Decision == protocol.DecisionDeny || got.Results[0].Decision != protocol.DecisionDeny || got.Message != "" {
		t.Fatalf("%+v", got)
	}
}

func TestContinueOnErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name, phase, onError string
		outcome              protocol.Outcome
		decision             protocol.Decision
		effective            protocol.Decision
		continued            bool
	}{
		{"content deny", "pre", "", protocol.OutcomeOK, protocol.DecisionDeny, protocol.DecisionAbstain, true},
		{"allow", "pre", "", protocol.OutcomeOK, protocol.DecisionAllow, protocol.DecisionAllow, false},
		{"abstain", "pre", "", protocol.OutcomeOK, protocol.DecisionAbstain, protocol.DecisionAbstain, false},
		{"default error deny", "pre", "", protocol.OutcomeError, "", protocol.DecisionAbstain, true},
		{"explicit error deny", "pre", "deny", protocol.OutcomeError, "", protocol.DecisionAbstain, true},
		{"error allow", "pre", "allow", protocol.OutcomeError, "", protocol.DecisionAllow, false},
		{"error ignore", "pre", "ignore", protocol.OutcomeError, "", protocol.DecisionAbstain, false},
		{"timeout", "pre", "deny", protocol.OutcomeTimeout, "", protocol.DecisionAbstain, true},
		{"canceled", "pre", "deny", protocol.OutcomeCanceled, "", protocol.DecisionAbstain, true},
		{"invalid response", "pre", "deny", protocol.OutcomeInvalidResponse, "", protocol.DecisionAbstain, true},
		{"skipped", "pre", "deny", protocol.OutcomeSkipped, "", protocol.DecisionAbstain, false},
		{"post deny", "post", "deny", protocol.OutcomeOK, protocol.DecisionDeny, protocol.DecisionAbstain, false},
		{"post error", "post", "deny", protocol.OutcomeError, "", protocol.DecisionAbstain, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := config.Hook{OnError: tc.onError, ContinueOnError: true}
			result := protocol.HookResult{Outcome: tc.outcome, Decision: tc.decision, Reason: "original reason", Message: "original message", Error: "original error"}
			want := result
			want.Effective, want.ContinuedOnError = tc.effective, tc.continued
			classify(tc.phase, h, &result)
			if !reflect.DeepEqual(result, want) {
				t.Fatalf("result: %+v; want %+v", result, want)
			}
			if tc.outcome != protocol.OutcomeOK {
				got := (&Runner{}).diagnostic(tc.phase, h, tc.outcome, "diagnostic reason")
				if got.Effective != tc.effective || got.ContinuedOnError != tc.continued || got.Outcome != tc.outcome || got.Reason != "diagnostic reason" {
					t.Fatalf("diagnostic: %+v", got)
				}
			}
		})
	}
}

func TestContinueOnErrorDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		outcome    protocol.Outcome
		decision   protocol.Decision
		reason     string
		message    string
		exitCode   int
	}{
		{"content deny", `printf '{"decision":"deny","reason":"policy reason","message":"denial message"}'`, protocol.OutcomeOK, protocol.DecisionDeny, "policy reason", "denial message", 0},
		{"error deny", "exit 7", protocol.OutcomeError, "", "", "", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := filepath.Join(t.TempDir(), "previous.json")
			first := hook("continued", script(t, tc.body))
			first.OnError, first.ContinueOnError = config.OnErrorDeny, true
			second := hook("dependent", script(t, `cat > "$CAPTURE"; printf '{"decision":"allow"}'`))
			second.DependsOn = []string{first.ID}
			second.Env = map[string]string{"CAPTURE": capture}
			got := (&Runner{Env: hermetic(t)}).Run(context.Background(), "pre", []config.Hook{first, second}, input())
			result := got.Results[0]
			if got.Decision != protocol.DecisionAllow || got.Message != "" || got.Results[1].Outcome != protocol.OutcomeOK ||
				result.Outcome != tc.outcome || result.Decision != tc.decision || result.Effective != protocol.DecisionAbstain ||
				!result.ContinuedOnError || result.Reason != tc.reason || result.Message != tc.message || result.ExitCode != tc.exitCode {
				t.Fatalf("%+v", got)
			}
			if tc.outcome != protocol.OutcomeOK && result.Error == "" {
				t.Fatalf("error not preserved: %+v", result)
			}
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			var decoded protocol.Input
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			result.Started = false // Process-start metadata is not part of the JSON protocol.
			if len(decoded.Previous) != 1 || !reflect.DeepEqual(decoded.Previous[0], result) {
				t.Fatalf("previous: %+v; want %+v", decoded.Previous, result)
			}
		})
	}
}

func TestMissingAndNonExecutable(t *testing.T) {
	dir := t.TempDir()
	noexec := filepath.Join(dir, "not-executable")
	if err := os.WriteFile(noexec, []byte("#!/bin/sh\nexit 0"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{filepath.Join(dir, "missing"), "missing-hook-command", noexec} {
		for _, optional := range []bool{false, true} {
			h := hook("missing", command)
			h.Optional = optional
			got := one(t, h, &Runner{Env: hermetic(t)})
			want := protocol.OutcomeError
			if optional {
				want = protocol.OutcomeSkipped
			}
			if got.Results[0].Outcome != want || !optional && got.Decision != protocol.DecisionDeny || optional && got.Decision != protocol.DecisionAbstain {
				t.Fatalf("%s optional=%v: %+v", command, optional, got)
			}
		}
	}
	h := hook("empty", "")
	h.Command = nil
	if got := one(t, h, &Runner{Env: hermetic(t)}); got.Results[0].Outcome != protocol.OutcomeError {
		t.Fatalf("%+v", got)
	}
}

func TestLimitsAndRedaction(t *testing.T) {
	body := `printf '{"decision":"allow"}'; printf '%s' '` + strings.Repeat(" ", 2048) + `'`
	h := hook("huge", script(t, body))
	h.MaxOutputBytes = 128
	got := one(t, h, &Runner{Env: hermetic(t)})
	if got.Results[0].Outcome != protocol.OutcomeInvalidResponse || !strings.Contains(got.Results[0].Error, "output limit") {
		t.Fatalf("%+v", got)
	}
	h = hook("stderr", script(t, `printf 'SECRET-`+strings.Repeat("x", 256)+`' >&2; printf '{"decision":"deny","reason":"SECRET","message":"SECRET"}'`))
	h.MaxOutputBytes = 128
	got = one(t, h, &Runner{Env: hermetic(t), Redact: func(s string) string { return strings.ReplaceAll(s, "SECRET", "[redacted]") }})
	v := got.Results[0]
	if !strings.Contains(v.Stderr, "[stderr truncated]") || strings.Contains(v.Stderr, "SECRET") || v.Reason != "[redacted]" || got.Message != "[redacted]" {
		t.Fatalf("%+v", got)
	}
	h = hook("bad-secret", script(t, `printf '{"decision":"SECRET"}'`))
	got = one(t, h, &Runner{Env: hermetic(t), Redact: func(s string) string { return strings.ReplaceAll(s, "SECRET", "[redacted]") }})
	if strings.Contains(got.Results[0].Error, "SECRET") {
		t.Fatalf("%+v", got)
	}
}

func TestInputEnvironmentAndResolution(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "input.json")
	vars := filepath.Join(dir, "env")
	path := script(t, `cat > "$CAPTURE"
printf '%s\n' "$GHOK_EVENT" "$GHOK_EVENT_ID" "$GHOK_HOOK_ID" "$GHOK_NESTED" "$GHOK_REAL_GH" "$CUSTOM" > "$VARS"
printf '{"decision":"allow"}'`)
	h := hook("environment", "./hook")
	h.Env = map[string]string{"CAPTURE": capture, "VARS": vars, "CUSTOM": "custom", "GHOK_NESTED": "wrong"}
	r := &Runner{BaseDir: filepath.Dir(path), Env: hermetic(t), RealGh: "/fake/gh"}
	got := one(t, h, r)
	if got.Decision != protocol.DecisionAllow {
		t.Fatalf("%+v", got)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var decoded protocol.Input
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Event != "pre" || decoded.HookID != h.ID || decoded.EventID != "test-event" || !reflect.DeepEqual(decoded.Operations, input().Operations) {
		t.Fatalf("%+v", decoded)
	}
	data, err = os.ReadFile(vars)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "pre\ntest-event\nenvironment\n1\n/fake/gh\ncustom\n" {
		t.Fatalf("env %q", data)
	}
	// The runner's supplied PATH, not the test process PATH, resolves bare names.
	h.Command = []string{"hook"}
	r.Env = append(r.Env, "PATH="+filepath.Dir(path)+":/usr/bin:/bin")
	if got = one(t, h, r); got.Decision != protocol.DecisionAllow {
		t.Fatalf("%+v", got)
	}
}

func TestSelectStableDependencies(t *testing.T) {
	disabled := false
	r := &Runner{Hooks: []config.Hook{
		{ID: "same1", Phase: "pre"}, {ID: "same2", Phase: "pre"},
		{ID: "base", Phase: "pre"},
		{ID: "dependent", Phase: "pre", DependsOn: []string{"base"}},
		{ID: "disabled", Phase: "pre", Enabled: &disabled}, {ID: "post", Phase: "post"},
		{ID: "mismatch", Phase: "pre", Match: config.Match{Effects: config.StringList{"read"}}},
		{ID: "maybe", Phase: "pre", Match: config.Match{Repositories: config.StringList{"org/*"}}},
	}}
	s := policy.Subject{Result: model.Result{Operations: input().Operations}}
	// As normalized for `pr merge 1` outside a repository.
	s.Result.Operations[0].Unresolved = []model.Unresolved{{Field: "target.repository", Reason: "repository from local git context", Source: "git"}}
	selected := r.Select("pre", s)
	var ids []string
	for _, h := range selected {
		ids = append(ids, h.ID)
	}
	if !reflect.DeepEqual(ids, []string{"same1", "same2", "base", "dependent", "maybe"}) {
		t.Fatalf("%v", ids)
	}
	s.Result.Operations[0].Execution = model.ExecutionSkipped
	if got := r.Select("pre", s); len(got) != 0 {
		t.Fatalf("skipped operation matched %v", got)
	}
}

func TestDependenciesAndPrevious(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "previous.json")
	first := hook("first", script(t, `printf '{"decision":"allow","reason":"first reason"}'`))
	second := hook("second", script(t, `cat > "$CAPTURE"; printf '{"decision":"allow"}'`))
	second.DependsOn = []string{"first"}
	second.Env = map[string]string{"CAPTURE": capture}
	r := &Runner{Hooks: []config.Hook{first, second}, Concurrency: 2, Env: hermetic(t)}
	selected := r.Select("pre", policy.Subject{Result: model.Result{Operations: input().Operations}})
	got := r.Run(context.Background(), "pre", selected, input())
	if got.Decision != protocol.DecisionAllow || got.Results[0].HookID != "first" || got.Results[1].HookID != "second" {
		t.Fatalf("%+v", got)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var decoded protocol.Input
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Previous) != 1 || decoded.Previous[0].HookID != first.ID ||
		decoded.Previous[0].Outcome != protocol.OutcomeOK || decoded.Previous[0].Decision != protocol.DecisionAllow ||
		decoded.Previous[0].Effective != protocol.DecisionAllow || decoded.Previous[0].Reason != "first reason" {
		t.Fatalf("previous: %+v", decoded.Previous)
	}
	first.Command = []string{script(t, "exit 4")}
	first.OnError = "ignore"
	for _, optional := range []bool{false, true} {
		second.Optional = optional
		got = r.Run(context.Background(), "pre", []config.Hook{first, second}, input())
		want := protocol.OutcomeSkipped
		if optional {
			want = protocol.OutcomeOK
		}
		if got.Results[1].Outcome != want {
			t.Fatalf("optional %v: %+v", optional, got)
		}
	}
	second.DependsOn = []string{"missing"}
	if got = r.Run(context.Background(), "pre", []config.Hook{second}, input()); got.Results[0].Outcome != protocol.OutcomeSkipped {
		t.Fatalf("%+v", got)
	}
	first.DependsOn = []string{"second"}
	second.DependsOn = []string{"first"}
	got = r.Run(context.Background(), "pre", []config.Hook{first, second}, input())
	if got.Results[0].Outcome != protocol.OutcomeSkipped || got.Results[1].Outcome != protocol.OutcomeSkipped {
		t.Fatalf("%+v", got)
	}
	got = r.Run(context.Background(), "pre", []config.Hook{first, first}, input())
	if got.Results[0].Outcome != protocol.OutcomeSkipped || got.Results[1].Outcome != protocol.OutcomeSkipped {
		t.Fatalf("%+v", got)
	}
}

func waitFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return data
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatalf("file not ready: %s", path)
		case <-ticker.C:
		}
	}
}

func TestTimeoutAndCanceledProcessGroups(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent-cancel=%v", cancelParent), func(t *testing.T) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "pid")
			// wait in the TERM trap reaps the background descendant on macOS/Linux.
			path := script(t, `trap 'wait; exit 0' TERM
export GROUP_PID=$$
sh -c 'trap "wait; exit 0" TERM; sleep 30 & printf "%s\n" "$GROUP_PID" > "$PIDFILE"; wait' &
wait`)
			h := hook("tree", path)
			h.Env = map[string]string{"PIDFILE": pidFile}
			if !cancelParent {
				h.Timeout = config.Duration(2 * time.Second)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &Runner{Env: hermetic(t)}
			done := make(chan Aggregate, 1)
			go func() { done <- r.Run(ctx, "pre", []config.Hook{h}, input()) }()
			pidText := waitFile(t, pidFile)
			pid, err := strconv.Atoi(strings.TrimSpace(string(pidText)))
			if err != nil {
				t.Fatal(err)
			}
			if cancelParent {
				cancel()
			}
			var got Aggregate
			select {
			case got = <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("hook tree did not terminate")
			}
			want := protocol.OutcomeTimeout
			if cancelParent {
				want = protocol.OutcomeCanceled
			}
			if got.Results[0].Outcome != want || got.Results[0].DurationMs < 0 {
				t.Fatalf("%+v", got)
			}
			// Killed members can linger briefly as zombies until init/launchd
			// reaps them (macOS then reports EPERM), so wait a bounded time.
			deadline := time.Now().Add(5 * time.Second)
			for {
				err := syscall.Kill(-pid, 0)
				if errors.Is(err, syscall.ESRCH) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("process group %d remains: %v", pid, err)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

func TestSequentialDenyCannotBeOverridden(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "not-started")
	a := hook("allow", script(t, `printf '{"decision":"allow"}'`))
	d := hook("deny", script(t, `printf '{"decision":"deny","reason":"blocked"}'`))
	last := hook("last", script(t, `printf ran > "$MARKER"; printf '{"decision":"allow"}'`))
	last.Env = map[string]string{"MARKER": marker}
	got := (&Runner{Env: hermetic(t)}).Run(context.Background(), "pre", []config.Hook{a, d, last}, input())
	if got.Decision != protocol.DecisionDeny || got.Message != "blocked" || got.Results[2].Outcome != protocol.OutcomeSkipped || got.Results[2].Reason != "after pre-hook denial" {
		t.Fatalf("%+v", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("pending hook started: %v", err)
	}
}

func TestConcurrencyAndDeterministicAggregation(t *testing.T) {
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	var hooks []config.Hook
	for i := range 3 {
		body := `printf ready > "$READY"; while [ ! -f "$RELEASE" ]; do sleep 0.02; done; printf '{"decision":"allow"}'`
		h := hook(fmt.Sprintf("h%d", i), script(t, body))
		h.Env = map[string]string{"READY": filepath.Join(dir, fmt.Sprintf("ready%d", i)), "RELEASE": release}
		h.Background = true
		hooks = append(hooks, h)
	}
	r := &Runner{Concurrency: 3, Env: hermetic(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan Aggregate, 1)
	go func() { done <- r.Run(ctx, "pre", hooks, input()) }()
	// All hooks must start before any is released: proves actual parallelism.
	for i := range hooks {
		waitFile(t, hooks[i].Env["READY"])
	}
	if err := os.WriteFile(release, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.Decision != protocol.DecisionAllow {
		t.Fatalf("%+v", got)
	}
	for i, h := range hooks {
		if got.Results[i].HookID != h.ID || got.Results[i].Outcome != protocol.OutcomeOK {
			t.Fatalf("%+v", got)
		}
	}
	// A later hook waits for the earlier hook to be running, then denies first.
	first := hook("earlier", script(t, `printf ready > "$READY"; while [ ! -f "$DENIED" ]; do sleep 0.02; done; sleep 0.1; printf '{"decision":"allow"}'`))
	first.OnError, first.Background = "ignore", true
	first.Env = map[string]string{"READY": filepath.Join(dir, "earlier-ready"), "DENIED": filepath.Join(dir, "later-denied")}
	deny := hook("later", script(t, `while [ ! -f "$READY" ]; do sleep 0.02; done; printf denied > "$DENIED"; printf '{"decision":"deny","message":"later denial"}'`))
	deny.Env = first.Env
	got = r.Run(ctx, "pre", []config.Hook{first, deny}, input())
	if got.Decision != protocol.DecisionDeny || got.Message != "later denial" || got.Results[0].HookID != "earlier" || got.Results[0].Outcome != protocol.OutcomeOK || got.Results[1].Outcome != protocol.OutcomeOK {
		t.Fatalf("%+v", got)
	}
}

func TestPreDenialDoesNotCancelRunningHooks(t *testing.T) {
	for _, tc := range []struct {
		name            string
		siblingFirst    bool
		siblingDecision protocol.Decision
		message         string
	}{
		{"background sibling", true, protocol.DecisionAllow, "first denial"},
		{"foreground sibling", false, protocol.DecisionAllow, "first denial"},
		{"first denial in selection order", true, protocol.DecisionDeny, "sibling denial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			env := map[string]string{
				"READY":    filepath.Join(dir, "ready"),
				"DENIED":   filepath.Join(dir, "denied"),
				"DONE":     filepath.Join(dir, "done"),
				"PENDING":  filepath.Join(dir, "pending"),
				"DECISION": string(tc.siblingDecision),
			}
			sibling := hook("sibling", script(t, `printf ready > "$READY"; while [ ! -f "$DENIED" ]; do sleep 0.02; done; sleep 0.1; printf done > "$DONE"; printf '{"decision":"%s","message":"sibling denial"}' "$DECISION"`))
			deny := hook("deny", script(t, `while [ ! -f "$READY" ]; do sleep 0.02; done; printf denied > "$DENIED"; printf '{"decision":"deny","message":"first denial"}'`))
			pending := hook("pending", script(t, `printf ran > "$PENDING"`))
			sibling.Env, deny.Env, pending.Env = env, env, env
			selected := []config.Hook{sibling, deny, pending}
			siblingIndex := 0
			if !tc.siblingFirst {
				selected = []config.Hook{deny, sibling, pending}
				siblingIndex = 1
			}
			selected[0].Background = true
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			got := (&Runner{Concurrency: 2, Env: hermetic(t)}).Run(ctx, "pre", selected, input())
			if got.Decision != protocol.DecisionDeny || got.Message != tc.message || got.Results[siblingIndex].Outcome != protocol.OutcomeOK ||
				got.Results[siblingIndex].Decision != tc.siblingDecision || !got.Results[siblingIndex].Started {
				t.Fatalf("%+v", got)
			}
			for i, h := range selected {
				if got.Results[i].HookID != h.ID {
					t.Fatalf("result order: %+v", got.Results)
				}
			}
			skipped := got.Results[2]
			if skipped.Outcome != protocol.OutcomeSkipped || skipped.Reason != "after pre-hook denial" ||
				skipped.Effective != protocol.DecisionAbstain || skipped.Started || skipped.ContinuedOnError {
				t.Fatalf("pending: %+v", skipped)
			}
			if _, err := os.Stat(env["DONE"]); err != nil {
				t.Fatalf("running sibling did not finish: %v", err)
			}
			if _, err := os.Stat(env["PENDING"]); !os.IsNotExist(err) {
				t.Fatalf("pending hook started: %v", err)
			}
		})
	}
}

// Foreground hooks wait for earlier foreground hooks and their Wait list.
func TestParallelSteps(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	var hooks []config.Hook
	for _, spec := range []struct {
		id       string
		parallel bool
	}{{"a", false}, {"b", true}, {"c", true}, {"d", false}} {
		h := hook(spec.id, script(t, `printf '%s+\n' "$ID" >> "$LOG"; sleep 0.2; printf '%s-\n' "$ID" >> "$LOG"; printf '{"decision":"allow"}'`))
		h.Env = map[string]string{"ID": spec.id, "LOG": log}
		h.Background = spec.parallel
		if spec.id == "d" {
			h.Wait = []string{"b", "c"} // end of a parallel block
		}
		hooks = append(hooks, h)
	}
	r := &Runner{Concurrency: 4, Env: hermetic(t)}
	if got := r.Run(context.Background(), "pre", hooks, input()); got.Decision != protocol.DecisionAllow {
		t.Fatalf("%+v", got)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	pos := map[string]int{}
	for i, line := range lines {
		pos[line] = i
	}
	// a finishes before b/c start; b and c overlap; d starts after both end.
	if len(lines) != 8 || pos["a-"] > pos["b+"] || pos["a-"] > pos["c+"] || pos["b+"] > pos["c-"] || pos["c+"] > pos["b-"] || pos["d+"] < pos["b-"] || pos["d+"] < pos["c-"] {
		t.Fatalf("order %v", lines)
	}
}

func TestAlreadyCanceledAndInputError(t *testing.T) {
	r := &Runner{Env: hermetic(t)}
	h := hook("never", script(t, "exit 0"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := r.Run(ctx, "pre", []config.Hook{h}, input())
	if got.Results[0].Outcome != protocol.OutcomeCanceled {
		t.Fatalf("%+v", got)
	}
	in := input()
	in.Operations[0].Parameters = map[string]any{"bad": make(chan int)}
	got = r.Run(context.Background(), "pre", []config.Hook{h}, in)
	if got.Results[0].Outcome != protocol.OutcomeError || !strings.Contains(got.Results[0].Error, "encode hook input") {
		t.Fatalf("%+v", got)
	}
}

func BenchmarkSelectNoHooks(b *testing.B) {
	r := &Runner{}
	s := policy.Subject{Result: model.Result{Operations: input().Operations}}
	b.ReportAllocs()
	for b.Loop() {
		r.Select("pre", s)
	}
}
func BenchmarkSelectNoMatchingHooks(b *testing.B) {
	r := &Runner{Hooks: []config.Hook{{ID: "read", Phase: "pre", Match: config.Match{Effects: config.StringList{"read"}}}}}
	s := policy.Subject{Result: model.Result{Operations: input().Operations}}
	b.ReportAllocs()
	for b.Loop() {
		r.Select("pre", s)
	}
}
func BenchmarkRunOneTrivialHook(b *testing.B) {
	h := hook("trivial", script(b, "exit 0"))
	r := &Runner{Env: hermetic(b)}
	in := input()
	b.ReportAllocs()
	for b.Loop() {
		if got := r.Run(context.Background(), "pre", []config.Hook{h}, in); got.Results[0].Outcome != protocol.OutcomeOK {
			b.Fatal(got)
		}
	}
}

func TestHookWarningOnlySuccessfulNonDeny(t *testing.T) {
	for _, decision := range []string{"allow", "abstain", "deny", "warn"} {
		for _, continued := range []bool{false, true} {
			h := hook("warning", script(t, fmt.Sprintf(`printf '{"decision":%q,"warning":"review this"}'`, decision)))
			h.ContinueOnError = continued
			got := one(t, h, &Runner{Env: hermetic(t)}).Results[0]
			want := ""
			if decision == "allow" || decision == "abstain" {
				want = "review this"
			}
			if got.Warning != want {
				t.Fatalf("%s continued=%t: %+v", decision, continued, got)
			}
			if decision == "warn" && got.Outcome != protocol.OutcomeInvalidResponse {
				t.Fatalf("warn became a hook decision: %+v", got)
			}
		}
	}
	h := hook("failed", script(t, `printf '{"decision":"allow","warning":"discard"}'; exit 3`))
	if got := one(t, h, &Runner{Env: hermetic(t)}).Results[0]; got.Warning != "" || got.Outcome != protocol.OutcomeError {
		t.Fatalf("failed warning escaped: %+v", got)
	}
}

func TestHookWarningRedaction(t *testing.T) {
	secret := "warning-" + "credential"
	h := hook("warning", script(t, fmt.Sprintf(`printf '{"decision":"allow","warning":%q}'`, secret)))
	r := &Runner{Env: hermetic(t), Redact: func(s string) string { return strings.ReplaceAll(s, secret, "[REDACTED]") }}
	if got := one(t, h, r).Results[0]; got.Warning != "[REDACTED]" {
		t.Fatalf("warning not redacted: %+v", got)
	}
}

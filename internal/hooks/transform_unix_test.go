//go:build unix

package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/protocol"
)

func TestTransformRuntimeOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		outcome    protocol.Outcome
	}{
		{"empty", "cat >/dev/null", protocol.OutcomeOK},
		{"edit", `cat >/dev/null; printf '{"schemaVersion":1,"set":{"/operations/0/texts/title/value":"new"}}'`, protocol.OutcomeOK},
		{"hook response", `printf '{"decision":"allow"}'`, protocol.OutcomeInvalidResponse},
		{"trailing", `printf '{"schemaVersion":1}{}'`, protocol.OutcomeInvalidResponse},
		{"nonzero", `printf '{"schemaVersion":1}'; exit 9`, protocol.OutcomeError},
		{"overflow", `dd if=/dev/zero bs=1048576 count=2 2>/dev/null`, protocol.OutcomeInvalidResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Runner{Env: hermetic(t)}
			response, result := r.Transform(context.Background(), config.Transform{ID: "edit", Command: []string{script(t, tc.body)}}, input())
			if result.Outcome != tc.outcome || result.Phase != "pre" {
				t.Fatalf("response=%+v result=%+v", response, result)
			}
			if result.Outcome != protocol.OutcomeOK && response.SchemaVersion != 0 {
				t.Fatalf("failed response escaped: %+v", response)
			}
		})
	}
}

func TestTransformEnvelopeEnvironmentAndRedaction(t *testing.T) {
	for _, masked := range []bool{false, true} {
		t.Run(map[bool]string{false: "raw", true: "redacted"}[masked], func(t *testing.T) {
			dir := t.TempDir()
			payload, environment := filepath.Join(dir, "input"), filepath.Join(dir, "env")
			path := script(t, `cat > "$PAYLOAD"; printf '%s\n' "$GHOK_EVENT" "$GHOK_HOOK_ID" "$GHOK_NESTED" "$GHOK_NESTED_TOKEN" "$CUSTOM" "$GHOK_EVENT_SOURCE" > "$ENVIRONMENT"`)
			secret := "ghp_" + strings.Repeat("a", 36)
			r := Runner{Env: append(hermetic(t), "GH_TOKEN="+secret, "GHOK_NESTED_TOKEN=original"), Source: "test"}
			in := input()
			in.Invocation.Argv = []string{secret}
			in.Previous = []protocol.HookResult{{HookID: "old"}}
			in.Result = &protocol.ExecResult{}
			_, result := r.Transform(context.Background(), config.Transform{ID: "edit", Command: []string{path}, RedactInput: masked, Env: map[string]string{"PAYLOAD": payload, "ENVIRONMENT": environment, "CUSTOM": "override", "GHOK_NESTED_TOKEN": "forged"}}, in)
			if result.Outcome != protocol.OutcomeOK {
				t.Fatal(result)
			}
			data, err := os.ReadFile(payload)
			if err != nil {
				t.Fatal(err)
			}
			var got protocol.Input
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.Event != "transform" || got.Phase != "pre" || got.HookID != "edit" || got.SchemaVersion != 1 || len(got.Previous) != 0 || got.Result != nil {
				t.Fatalf("envelope: %s", data)
			}
			if strings.Contains(string(data), secret) == masked || (got.Redactions > 0) != masked {
				t.Fatalf("redaction=%t input=%s", masked, data)
			}
			data, err = os.ReadFile(environment)
			if err != nil || string(data) != "transform\nedit\n1\noriginal\noverride\ntest\n" {
				t.Fatalf("environment: %s %v", data, err)
			}
			if in.Invocation.Argv[0] != secret || len(in.Previous) != 1 || in.Result == nil {
				t.Fatal("mutated original input")
			}
		})
	}
}

func TestTransformTimeoutAndCancellation(t *testing.T) {
	path := script(t, "sleep 30")
	r := Runner{Env: hermetic(t)}
	_, got := r.Transform(context.Background(), config.Transform{ID: "edit", Command: []string{path}, Timeout: config.Duration(20 * time.Millisecond)}, input())
	if got.Outcome != protocol.OutcomeTimeout || got.Effective != protocol.DecisionDeny {
		t.Fatalf("timeout: %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, got = r.Transform(ctx, config.Transform{ID: "edit", Command: []string{path}, OnError: config.OnErrorAllow}, input())
	if got.Outcome != protocol.OutcomeCanceled || got.Started || got.Effective != protocol.DecisionAllow {
		t.Fatalf("cancel: %+v", got)
	}
}

func TestTransformSerialCalls(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "finished")
	r := Runner{Env: hermetic(t)}
	for _, body := range []string{`sleep 0.02; printf finished > "$MARKER"`, `test "$(cat "$MARKER")" = finished`} {
		_, result := r.Transform(context.Background(), config.Transform{ID: "edit", Command: []string{script(t, body)}, Env: map[string]string{"MARKER": marker}}, input())
		if result.Outcome != protocol.OutcomeOK {
			t.Fatalf("serial: %+v", result)
		}
	}
}

func TestTransformWarningPropagation(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{`printf '{"schemaVersion":1,"warning":"review this"}'`, true},
		{`printf '{"schemaVersion":1,"warning":"review this","set":{"/operations/0/target/number":2}}'`, true},
		{`printf '{"schemaVersion":1,"warning":"review this"}'; exit 3`, false},
		{`printf '{"schemaVersion":1,"warning":"review this","unknown":true}'`, false},
		{`printf '{"schemaVersion":1,"warning":42}'`, false},
	} {
		r := Runner{Env: hermetic(t)}
		response, result := r.Transform(context.Background(), config.Transform{ID: "warning", Command: []string{script(t, tc.body)}}, input())
		if tc.ok {
			if result.Outcome != protocol.OutcomeOK || result.Warning != "review this" || response.Warning != "review this" {
				t.Fatalf("warning lost: response=%+v result=%+v", response, result)
			}
		} else if result.Outcome == protocol.OutcomeOK || result.Warning != "" || response.Warning != "" {
			t.Fatalf("failed warning escaped: response=%+v result=%+v", response, result)
		}
	}
}

func TestTransformWarningRedactedDiagnostic(t *testing.T) {
	secret := "warning-" + "credential"
	r := Runner{Env: hermetic(t), Redact: func(s string) string { return strings.ReplaceAll(s, secret, "[REDACTED]") }}
	body := `printf '{"schemaVersion":1,"warning":"` + secret + `"}'`
	response, result := r.Transform(context.Background(), config.Transform{ID: "warning", Command: []string{script(t, body)}}, input())
	if result.Warning != "[REDACTED]" || response.Warning != secret {
		t.Fatalf("warning diagnostic or original response changed: response=%+v result=%+v", response, result)
	}
}

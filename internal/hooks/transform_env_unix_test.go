//go:build unix

package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/protocol"
	"github.com/mkusaka/ghok/internal/redact"
)

func TestTransformLearnsEnvironmentBeforeDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, variable, exit string
		outcome              protocol.Outcome
	}{
		{"valid", "FREE_CONTEXT", "0", protocol.OutcomeOK},
		{"forbidden", "PATH", "0", protocol.OutcomeInvalidResponse},
		{"process-error", "FREE_CONTEXT", "9", protocol.OutcomeError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			secret := "minted-" + "opaque-value"
			response, err := json.Marshal(map[string]any{"schemaVersion": 1, "env": map[string]any{tc.variable: secret}})
			if err != nil {
				t.Fatal(err)
			}
			r := redact.New(nil)
			runner := Runner{Env: hermetic(t), Redactor: r}
			_, result := runner.Transform(context.Background(), config.Transform{
				ID: "mint", Command: []string{script(t, `printf '%s' "$VALUE" >&2; printf '%s' "$RESPONSE"; exit "$STATUS"`)},
				Env: map[string]string{"VALUE": secret, "RESPONSE": string(response), "STATUS": tc.exit},
			}, input())
			if result.Outcome != tc.outcome || result.Stderr != "[REDACTED]" || strings.Contains(result.Error, secret) {
				t.Fatalf("unsafe transform diagnostics: %+v", result)
			}
			// A later redactInput hook must mask the value even when PRE still
			// runs in the wrapper's original environment, or the chain rolled back.
			in := input()
			in.Invocation.Argv = []string{"version", secret}
			got := PrepareInput(config.PhasePre, config.Hook{RedactInput: true}, in, r)
			if got.Invocation.Argv[1] != "[REDACTED]" {
				t.Fatal("learned environment secret was lost")
			}
		})
	}
}

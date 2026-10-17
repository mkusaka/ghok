//go:build unix

package hooks

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/protocol"
	"github.com/mkusaka/ghok/internal/redact"
)

// Transform executes exactly one serial request transform. Scheduling and
// rollback belong to the caller; policy hook decisions are not accepted.
func (r *Runner) Transform(ctx context.Context, t config.Transform, in protocol.Input) (protocol.TransformResponse, protocol.HookResult) {
	h := config.Hook{ID: t.ID, Phase: config.PhasePre, Command: t.Command, Env: t.Env, Timeout: t.Timeout, OnError: t.OnError, RedactInput: t.RedactInput}
	if h.Timeout == 0 {
		h.Timeout = config.Duration(config.DefaultTransformTimeout)
	}
	if h.OnError == "" {
		h.OnError = config.OnErrorDeny
	}
	in.SchemaVersion = 1
	in.Event = protocol.EventTransform
	in.Phase = config.PhasePre
	in.HookID = t.ID
	in.Previous = nil
	in.Result = nil
	in.Redactions = 0
	if t.RedactInput {
		base := r.Env
		if base == nil {
			base = os.Environ()
		}
		env := make(map[string]string, len(base))
		for _, entry := range base {
			if key, value, ok := strings.Cut(entry, "="); ok {
				env[key] = value
			}
		}
		inputRedactor := r.Redactor
		if inputRedactor == nil {
			inputRedactor = redact.New(env)
		}
		in = inputRedactor.Input(in)
	}
	var response protocol.TransformResponse
	result := r.executeResponse(ctx, config.PhasePre, h, in, func(data []byte) error {
		// Learn values before executeResponse redacts stderr/errors, including
		// responses rejected by strict validation or a nonzero process exit.
		if r.Redactor != nil {
			var fields struct {
				Env map[string]json.RawMessage `json:"env"`
			}
			if json.Unmarshal(data, &fields) == nil {
				values := make([]string, 0, len(fields.Env))
				for _, raw := range fields.Env {
					var value string
					if json.Unmarshal(raw, &value) == nil {
						values = append(values, value)
					}
				}
				if len(values) > 0 {
					r.Redactor.AddSecrets(values...)
				}
			}
		}
		var err error
		response, err = protocol.ParseTransformResponse(data)
		return err
	})
	if result.Outcome != protocol.OutcomeOK {
		response = protocol.TransformResponse{}
	} else {
		result.Warning = r.redact(response.Warning)
	}
	return response, result
}

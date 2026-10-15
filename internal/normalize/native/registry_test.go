package native

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

func TestUncuratedGeneratedCommandIsUnknown(t *testing.T) {
	// Model regeneration discovering an unreviewed leaf or command group.
	// Rebuild the actual startup join rather than hand-constructing its fallback.
	for _, path := range []string{"pr view", "pr"} {
		t.Run(path, func(t *testing.T) {
			curation := curatedCommands[path]
			previous := registry
			delete(curatedCommands, path)
			registry = buildRegistry()
			t.Cleanup(func() {
				curatedCommands[path] = curation
				registry = previous
			})
			for _, flag := range []string{"", "--help", "--version"} {
				argv := strings.Fields(path)
				if flag != "" {
					argv = append(argv, flag)
				}
				r := Normalize(model.Invocation{}, argv)
				op := r.Operations[0]
				if op.Family != model.FamilyUnknown || op.Action != model.FamilyUnknown || op.Effect != model.EffectUnknown || op.UnknownReason != model.UnknownCommand || op.Detail != "gh command not yet classified by ghok" || r.Classification.MutationFree || !reflect.DeepEqual(op.Origin.Command, strings.Fields(path)) {
					t.Fatalf("uncurated command became known or safe: %+v", r)
				}
			}
		})
	}
}

func TestNormalizeTypedFlags(t *testing.T) {
	for _, tt := range []struct {
		argv []string
		key  string
		want any
	}{
		{[]string{"pr", "list", "--limit=0x10"}, "limit", "16"},
		{[]string{"codespace", "delete", "--days=65535"}, "days", "65535"},
		{[]string{"run", "view", "--attempt=18446744073709551615"}, "attempt", "18446744073709551615"},
		{[]string{"project", "item-edit", "--number=1.25"}, "number", "1.25"},
		{[]string{"codespace", "create", "--idle-timeout=1m30s"}, "idleTimeout", "1m30s"},
		{[]string{"pr", "create", "--label=bug,docs", "--label=help wanted"}, "label", []string{"bug", "docs", "help wanted"}},
		{[]string{"workflow", "run", "ci.yml", "--raw-field=message=one,two", "--raw-field=mode=fast"}, "inputs", map[string]any{"message": "one,two", "mode": "fast"}},
	} {
		t.Run(strings.Join(tt.argv, " "), func(t *testing.T) {
			r := Normalize(model.Invocation{}, tt.argv)
			if r.Classification.Confidence != model.ConfidenceExact || !reflect.DeepEqual(r.Operations[0].Parameters[tt.key], tt.want) {
				t.Fatalf("result=%+v, want %s=%v", r, tt.key, tt.want)
			}
		})
	}
	for _, argv := range [][]string{
		{"pr", "list", "--limit=invalid", "--help"},
		{"codespace", "delete", "--days=65536", "--help"},
		{"codespace", "delete", "--days=-1"},
		{"run", "view", "--attempt=18446744073709551616", "--help"},
		{"project", "item-edit", "--number=invalid", "--help"},
		{"codespace", "create", "--idle-timeout=invalid", "--help"},
		{"pr", "create", "--label=\"unterminated", "--help"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			r := Normalize(model.Invocation{}, argv)
			if r.Classification.MutationFree || r.Classification.Confidence != model.ConfidenceHeuristic || len(r.Errors) == 0 || r.Operations[0].Family == "help" {
				t.Fatalf("invalid typed value became safe: %+v", r)
			}
		})
	}
}

func TestNormalizeDiscoveredCommands(t *testing.T) {
	for _, tt := range []struct {
		argv   []string
		action string
		effect model.Effect
	}{
		{[]string{"a11y"}, "accessibility", model.EffectRead},
		{[]string{"at", "inspect", "bundle.jsonl"}, "native.attestation.inspect", model.EffectRead},
		{[]string{"auth", "git-credential", "get"}, "native.auth.git-credential", model.EffectRead},
		{[]string{"cs", "select", "--repo=o/r"}, "native.codespace.select", model.EffectRead},
		{[]string{"repo", "credits", "o/r"}, "native.repo.credits", model.EffectRead},
		{[]string{"aw", "--help"}, "native.aw", model.EffectUnknown},
		{[]string{"stack", "--help"}, "native.stack", model.EffectUnknown},
		{[]string{"webhook", "--help"}, "native.webhook", model.EffectUnknown},
		{[]string{"co", "42", "--force"}, "pr.checkout", model.EffectWrite},
		{[]string{"__completeNoDesc", "pr", "--help"}, "native.completion", model.EffectUnknown},
	} {
		t.Run(strings.Join(tt.argv, " "), func(t *testing.T) {
			r := Normalize(model.Invocation{}, tt.argv)
			op := r.Operations[0]
			if op.Action != tt.action || op.Effect != tt.effect || r.Classification.Confidence != model.ConfidenceExact || r.Classification.MutationFree != (tt.effect == model.EffectRead) {
				t.Fatalf("result=%+v", r)
			}
			if tt.action == "native.auth.git-credential" && op.Parameters["sensitive"] != true {
				t.Fatalf("credential helper must be sensitive: %+v", op)
			}
			if (tt.action == "native.repo.credits" || tt.action == "native.codespace.select") && op.Target.Repository != "o/r" {
				t.Fatalf("missing repository selector: %+v", op)
			}
			if tt.effect == model.EffectUnknown && !reflect.DeepEqual(op.Parameters["arguments"], tt.argv[1:]) {
				t.Fatalf("forwarded flags must remain arguments: %+v", op)
			}
		})
	}
}

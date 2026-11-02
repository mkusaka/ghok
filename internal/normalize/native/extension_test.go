package native

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

func TestDeclaredExtension(t *testing.T) {
	inv := model.Invocation{Extensions: map[string]model.Effect{"dash": model.EffectRead, "stack": "", "label": model.EffectWrite}}
	direct, ok := DeclaredExtension(inv, []string{"dash", "-R", "o/r"})
	if !ok {
		t.Fatal("declared extension not recognized")
	}
	execd := Normalize(inv, []string{"ext", "exec", "dash", "-R", "o/r"})
	for _, res := range []model.Result{direct, execd} {
		op := res.Operations[0]
		if op.Action != "extension.dash" || op.Effect != model.EffectRead || op.UnknownReason != model.UnknownNone ||
			!slices.Equal(op.Origin.Command, []string{"dash"}) || op.Parameters != nil ||
			res.Classification.MutationFree {
			t.Fatalf("%+v %+v", res.Classification, op)
		}
	}
	// Undeclared effect stays unknown and keeps unknown-mutation handling.
	if op := Normalize(inv, []string{"extension", "exec", "stack", "sync"}).Operations[0]; op.Effect != model.EffectUnknown || op.UnknownReason != model.UnknownExtension {
		t.Fatalf("%+v", op)
	}
	// Built-ins shadow direct spellings; only extension exec reaches them.
	if _, ok := DeclaredExtension(inv, []string{"label", "list"}); ok {
		t.Fatal("built-in label must not become an extension")
	}
	if op := Normalize(inv, []string{"extension", "exec", "label"}).Operations[0]; op.Action != "extension.label" {
		t.Fatalf("%+v", op)
	}
	if _, ok := DeclaredExtension(inv, []string{"other"}); ok {
		t.Fatal("undeclared name must stay unknown")
	}
	// Installed official extensions shadow their install stubs.
	if res, ok := DeclaredExtension(inv, []string{"stack", "push"}); !ok || res.Operations[0].Action != "extension.stack" {
		t.Fatalf("%v %+v", ok, res)
	}
	// Root flags precede the command, as in Cobra Find; default alias co is
	// registered after extensions.
	inv.Extensions["co"] = model.EffectWrite
	for want, argv := range map[string][]string{"extension.dash": {"--help", "dash", "push"}, "extension.co": {"co", "--help"}} {
		if res, ok := DeclaredExtension(inv, argv); !ok || res.Operations[0].Action != want {
			t.Fatalf("%v: %v %+v", argv, ok, res)
		}
	}
	if op := direct.Operations[0]; len(op.Unresolved) != 2 || op.Unresolved[1].Field != "target.host" || op.Unresolved[1].Source != "argv" {
		t.Fatalf("target must stay argv-unresolved: %+v", op.Unresolved)
	}
}

func TestDeclaredExtensionAliases(t *testing.T) {
	inv := model.Invocation{
		Extensions: map[string]model.Effect{"dash": model.EffectWrite},
		Aliases:    map[string]string{"s": "dash", "dash": "pr list"},
	}
	// An alias chain reaching a declared name stops there; a same-named alias
	// makes it ambiguous (installed extension or alias), so it stays unknown.
	expanded, alias, reason := ExpandAlias([]string{"s", "x"}, ExtensionAliases(inv))
	res, ok := DeclaredExtension(inv, expanded)
	if reason != model.UnknownNone || alias != "s" || !slices.Equal(expanded, []string{"dash", "x"}) || !ok ||
		res.Operations[0].Family != model.FamilyUnknown || res.Operations[0].UnknownReason != model.UnknownExtension {
		t.Fatalf("%v %q %v %+v", expanded, alias, ok, res)
	}
	delete(inv.Aliases, "dash")
	expanded, _, _ = ExpandAlias([]string{"s"}, ExtensionAliases(inv))
	if res, ok := DeclaredExtension(inv, expanded); !ok || res.Operations[0].Action != "extension.dash" {
		t.Fatalf("%+v", res)
	}
}

// Stubs are the only top-level hidden DisableFlagParsing commands besides cobra
// internals (__complete); a new one after a gh bump belongs in
// officialExtensionStubs.
func TestOfficialExtensionStubsMatchRegistry(t *testing.T) {
	got := map[string]bool{}
	for path, c := range registry {
		if !strings.Contains(path, " ") && path != "" && !strings.HasPrefix(path, "__") && c.hidden && c.disableFlagParsing {
			got[path] = true
		}
	}
	if !maps.Equal(got, officialExtensionStubs) {
		t.Fatalf("registry stubs %v != officialExtensionStubs %v", got, officialExtensionStubs)
	}
}

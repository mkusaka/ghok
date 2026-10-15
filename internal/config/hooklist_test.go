package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHookListControlItems(t *testing.T) {
	c, err := Parse([]byte(`version: 1
hooks:
  pre:
    - {id: a, command: [a]}
    - parallel:
        - {id: b, command: [b]}
        - {id: c, command: [c], timeout: 2s}
    - {id: d, command: [d]}
    - {id: e, command: [e], background: true}
    - {id: f, command: [f], background: true}
    - wait: e
    - {id: g, command: [g]}
    - wait-all:
    - {id: h, command: [h]}
  post:
    - {id: i, command: [i]}
`), "/config")
	if err != nil {
		t.Fatal(err)
	}
	type got struct {
		id         string
		phase      string
		background bool
		wait       []string
	}
	var hooks []got
	for _, h := range c.Hooks {
		hooks = append(hooks, got{h.ID, h.Phase, h.Background, h.Wait})
	}
	want := []got{
		{"a", PhasePre, false, nil}, {"b", PhasePre, true, nil}, {"c", PhasePre, true, nil},
		{"d", PhasePre, false, []string{"b", "c"}}, {"e", PhasePre, true, []string{"b", "c"}}, {"f", PhasePre, true, []string{"b", "c"}},
		{"g", PhasePre, false, []string{"b", "c", "e"}}, {"h", PhasePre, false, []string{"b", "c", "e", "f"}},
		{"i", PhasePost, false, nil},
	}
	if !reflect.DeepEqual(hooks, want) {
		t.Fatalf("got %+v", hooks)
	}
	// Per-hook defaults still see the hook's own node inside a block.
	if c.Hooks[2].Timeout.D().String() != "2s" || c.Hooks[1].OnError != OnErrorDeny {
		t.Fatalf("defaults: %+v %+v", c.Hooks[1], c.Hooks[2])
	}
}

func TestHookListErrors(t *testing.T) {
	hook := "{id: x, command: [x]}"
	for item, want := range map[string]string{
		"- parallel: []": "non-empty list",
		"- parallel:\n      - parallel: [" + hook + "]": "not allowed inside parallel",
		"- parallel: [" + hook + "]\n    timeout: 5s":   "only key",
		"- wait: x":                   "not an earlier background hook in this phase list",
		"- " + hook + "\n  - wait: x": "not an earlier background hook in this phase list",
		"- wait-all: true":            "takes no value",
		"- parallel:\n      - {id: x, command: [x], bogus: 1}": "unknown key hooks.pre[0].parallel[0].bogus",
		"- {id: x, command: [x], bogus: 1}":                    "unknown key hooks.pre[0].bogus",
	} {
		_, err := Parse([]byte("version: 1\nhooks:\n  pre:\n    "+strings.ReplaceAll(item, "\n", "\n  ")+"\n"), "/config")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", item, err, want)
		}
	}
}

func TestHookListMustBeMapping(t *testing.T) {
	_, err := Parse([]byte("version: 1\nhooks:\n  - {id: a, phase: pre, command: [a]}\n"), "/config")
	want := "hooks must be a mapping with pre and post lists"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %q", err, want)
	}
}

func TestHookListPhaseKeyUnknown(t *testing.T) {
	_, err := Parse([]byte("version: 1\nhooks:\n  pre:\n    - {id: a, phase: post, command: [a]}\n"), "/config")
	want := "unknown key hooks.pre[0].phase"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %q", err, want)
	}
}

func TestHookListProjectParallelUnknownKey(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
	writeFixture(t, filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
	writeFixture(t, filepath.Join(repo, ".ghok.yaml"), `version: 1
hooks:
  pre:
    - parallel:
        - {id: a, command: [./hook], bogus: true}
`)
	c, notes, err := LoadEffective("", repo, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.IgnoredHooks) != 1 || c.IgnoredHooks[0].ID != "a" || len(c.Diagnostics) != 0 {
		t.Fatalf("unknown key discarded project hook: %+v, notes %v", c, notes)
	}
	want := "unknown key hooks.pre[0].parallel[0].bogus ignored"
	if !strings.Contains(strings.Join(notes, "\n"), want) {
		t.Fatalf("notes %v, want %q", notes, want)
	}
}

func TestHookListEmptyPhases(t *testing.T) {
	for _, hooks := range []string{"{}", "{pre: []}"} {
		t.Run(hooks, func(t *testing.T) {
			c, err := Parse([]byte("version: 1\nhooks: "+hooks+"\n"), "/config")
			if err != nil {
				t.Fatal(err)
			}
			if len(c.Hooks) != 0 {
				t.Fatalf("empty phase mapping produced hooks: %+v", c.Hooks)
			}
		})
	}
}

func TestHookListWaitCannotCrossPhases(t *testing.T) {
	_, err := Parse([]byte(`version: 1
hooks:
  pre:
    - {id: a, command: [a], background: true}
  post:
    - wait: a
    - {id: b, command: [b]}
`), "/config")
	want := "hooks.post[0].wait: a is not an earlier background hook in this phase list"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %q", err, want)
	}
}

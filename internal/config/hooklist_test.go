package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestHookListControlItems(t *testing.T) {
	c, err := Parse([]byte(`version: 1
hooks:
  - {id: a, phase: pre, command: [a]}
  - parallel:
      - {id: b, phase: pre, command: [b]}
      - {id: c, phase: pre, command: [c], timeout: 2s}
  - {id: d, phase: pre, command: [d]}
  - {id: e, phase: pre, command: [e], background: true}
  - {id: f, phase: pre, command: [f], background: true}
  - wait: e
  - {id: g, phase: pre, command: [g]}
  - wait-all:
  - {id: h, phase: post, command: [h]}
`), "/config")
	if err != nil {
		t.Fatal(err)
	}
	type got struct {
		id         string
		background bool
		wait       []string
	}
	var hooks []got
	for _, h := range c.Hooks {
		hooks = append(hooks, got{h.ID, h.Background, h.Wait})
	}
	want := []got{
		{"a", false, nil}, {"b", true, nil}, {"c", true, nil},
		{"d", false, []string{"b", "c"}}, {"e", true, []string{"b", "c"}}, {"f", true, []string{"b", "c"}},
		{"g", false, []string{"b", "c", "e"}}, {"h", false, []string{"b", "c", "e", "f"}},
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
	hook := "{id: x, phase: pre, command: [x]}"
	for item, want := range map[string]string{
		"- parallel: []": "non-empty list",
		"- parallel:\n      - parallel: [" + hook + "]": "not allowed inside parallel",
		"- parallel: [" + hook + "]\n    timeout: 5s":   "only key",
		"- wait: x":                   "not an earlier background hook",
		"- " + hook + "\n  - wait: x": "not an earlier background hook",
		"- wait-all: true":            "takes no value",
		"- parallel:\n      - {id: x, phase: pre, command: [x], bogus: 1}": "unknown key hooks[0].parallel[0].bogus",
		"- {id: x, phase: pre, command: [x], bogus: 1}":                    "unknown key hooks[0].bogus",
	} {
		_, err := Parse([]byte("version: 1\nhooks:\n  "+item+"\n"), "/config")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", item, err, want)
		}
	}
}

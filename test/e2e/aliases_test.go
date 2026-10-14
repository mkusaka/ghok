//go:build unix

package e2e

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mkusaka/ghok/internal/config"
)

func TestAliasesExpandForPolicyButPreserveExecutedArgv(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(map[bool]string{false: "allow", true: "deny"}[deny], func(t *testing.T) {
			s := newSandbox(t)
			if err := os.WriteFile(filepath.Join(s.env["GH_CONFIG_DIR"], "config.yml"), []byte("aliases:\n  land: pr merge --admin\n"), 0600); err != nil {
				t.Fatal(err)
			}
			s.configure(func(c *config.Config) {
				c.Policy.Mode = "enforce"
				c.Hooks = []config.Hook{s.hook("pre", "pre")}
				if deny {
					c.Policy.Rules = []config.Rule{{ID: "merge", Match: config.Match{Operations: config.StringList{"pr.merge.*"}}, Decision: "deny"}}
				}
			})
			args := []string{"land", "7", "-R", "acme/project"}
			result := s.gh(nil, args...)
			if deny {
				result.code(t, 1)
				s.absentRecord()
			} else {
				result.code(t, 0)
				s.onlyRecord(args, nil)
			}
			input := s.hookInput("pre")
			if input.Invocation.Alias != "land" || !reflect.DeepEqual(input.Invocation.Expanded, []string{"pr", "merge", "--admin", "7", "-R", "acme/project"}) || len(input.Operations) != 1 || input.Operations[0].Action != "pr.merge.direct" {
				t.Fatalf("expanded hook input=%+v", input)
			}
		})
	}
}

func TestBuiltinsDoNotLoadAliasesAndShellAliasesStayUnknown(t *testing.T) {
	for _, kind := range []string{"built-in", "shell-alias", "invalid-alias-config"} {
		t.Run(kind, func(t *testing.T) {
			s := newSandbox(t)
			body := []byte("aliases: [invalid")
			args := []string{"land", "7"}
			if kind == "shell-alias" {
				body = []byte("aliases:\n  land: '!printf unsafe'\n")
			}
			if kind == "built-in" {
				args = []string{"pr", "merge", "7", "--admin", "-R", "acme/project"}
			}
			if err := os.WriteFile(filepath.Join(s.env["GH_CONFIG_DIR"], "config.yml"), body, 0600); err != nil {
				t.Fatal(err)
			}
			s.configure(func(c *config.Config) {
				c.Policy.Mode = "enforce"
				c.Policy.UnknownMutation = "deny"
				c.Policy.Rules = []config.Rule{{ID: "merge", Match: config.Match{Operations: config.StringList{"pr.merge.*"}}, Decision: "deny"}}
				c.Hooks = []config.Hook{s.hook("pre", "pre")}
			})
			result := s.gh(nil, args...)
			result.code(t, 1)
			s.absentRecord()
			input := s.hookInput("pre")
			if kind == "built-in" {
				if len(input.Operations) != 1 || input.Operations[0].Action != "pr.merge.direct" {
					t.Fatal("built-in classification consulted malformed aliases")
				}
			} else if input.Classification.MutationFree {
				t.Fatal("unknown alias classified as safe")
			}
		})
	}
}

func TestDiscoveryRejectsAnotherGhokShim(t *testing.T) {
	s := newSandbox(t)
	other := filepath.Join(s.root, "other-shim")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ghokBinary, filepath.Join(other, "gh")); err != nil {
		t.Fatal(err)
	}
	s.env["PATH"] = s.bin + string(os.PathListSeparator) + other
	result := s.gh(nil, "version")
	result.code(t, 127)
	s.absentRecord()
	if _, err := os.Stat(filepath.Join(s.root, "pre.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("discovery launched a hook")
	}
}

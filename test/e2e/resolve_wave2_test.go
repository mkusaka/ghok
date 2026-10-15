//go:build unix

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/model"
)

func TestResolveWave2NodeSpecializationAndDiskCache(t *testing.T) {
	s := newSandbox(t)
	s.env["FAKEGH_API_RESPONSE"] = `{"data":{"node":{"__typename":"PullRequest","number":73,"repository":{"nameWithOwner":"org/repo"}}}}`
	args := []string{"normalize", "--allow-api", "--", "api", "graphql", "-f", `query=mutation { addComment(input:{subjectId:"opaque",body:"hello"}) { clientMutationId } }`}
	for range 2 {
		result := s.management(nil, args...)
		result.code(t, 0)
		var normalized model.Result
		if err := json.Unmarshal(result.stdout, &normalized); err != nil {
			t.Fatal(err)
		}
		op := normalized.Operations[0]
		if op.Action != "pr.comment.create" || op.Family != "pr.comment" || op.Target.Kind != "pullRequest" || op.Target.Repository != "org/repo" || op.Target.Number == nil || *op.Target.Number != 73 {
			t.Fatalf("resolved operation: %+v", op)
		}
		s.env["FAKEGH_API_RESPONSE"] = "invalid on a second network lookup"
	}
	if records := s.records(); len(records) != 1 || records[0].Argv[1] != "graphql" {
		t.Fatalf("resolver did not reuse disk cache: %+v", records)
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(s.cfg), "state", "nodes.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("cache permissions: %v, %v", info, err)
	}
}

func TestResolveWave2UnknownInputPolicy(t *testing.T) {
	for _, behavior := range []string{"allow", "audit", "deny"} {
		t.Run(behavior, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = "enforce"
				c.Policy.UnknownMutation = "deny"
				c.Policy.UnknownInput = behavior
			})
			result := s.gh(nil, "api", "graphql", "--input", "absent.json")
			if behavior == "deny" {
				result.code(t, 77)
				if len(s.records()) != 0 || !bytes.Contains(result.stderr, []byte("input-unavailable")) || !bytes.Contains(result.stderr, []byte("absent.json")) {
					t.Fatalf("missing denial cause: %s", result.stderr)
				}
			} else {
				result.code(t, 0)
				if len(s.records()) != 1 {
					t.Fatal("read failure incorrectly used unknownMutation")
				}
			}
		})
	}
}

func TestResolveWave2DefaultStdinTimeoutCause(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Policy.Mode = "enforce"
		c.Policy.UnknownMutation = "allow"
		c.Policy.UnknownInput = "deny"
	})
	cmd, cancel := s.command(filepath.Join(s.bin, "gh"), nil, "api", "graphql", "--input", "-")
	defer cancel()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close(); _ = write.Close() }()
	cmd.Stdin = read
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	result := outcome{err: cmd.Run(), stderr: stderr.Bytes()}
	result.code(t, 77)
	elapsed := time.Since(start)
	if elapsed < 2500*time.Millisecond || elapsed > 7*time.Second || !bytes.Contains(result.stderr, []byte("timeout")) || !bytes.Contains(result.stderr, []byte("unknownInput")) || len(s.records()) != 0 {
		t.Fatalf("stdin denial after %v: %s", elapsed, result.stderr)
	}
}

func TestResolveWave2OnUnresolvedManagementEvidence(t *testing.T) {
	for _, behavior := range []string{"deny", "ignore"} {
		t.Run(behavior, func(t *testing.T) {
			s := newSandbox(t)
			s.configure(func(c *config.Config) {
				c.Policy.Mode = "enforce"
				c.Policy.Rules = []config.Rule{{ID: "targeted", Decision: "deny", OnUnresolved: behavior, Match: config.Match{Repositories: config.StringList{"org/*"}, Operations: config.StringList{"comment.*"}}}}
			})
			args := []string{"api", "graphql", "-f", `query=mutation { addComment(input:{subjectId:"opaque",body:"hello"}) { clientMutationId } }`}
			result := s.management(nil, append([]string{"explain", "--no-resolve", "--"}, args...)...)
			result.code(t, 0)
			want := "allow"
			if behavior == "deny" {
				want = "deny"
				if !bytes.Contains(result.stdout, []byte("matched conservatively (target unresolved)")) {
					t.Fatalf("explain evidence missing: %s", result.stdout)
				}
			}
			if !bytes.Contains(result.stdout, []byte("Decision: "+want)) {
				t.Fatalf("explain: %s", result.stdout)
			}
			result = s.management(nil, append([]string{"hooks", "test", "--json", "--no-resolve", "--expect-policy", want, "--expect", want, "--"}, args...)...)
			result.code(t, 0)
			if behavior == "deny" && !bytes.Contains(result.stdout, []byte("matched conservatively (target unresolved)")) {
				t.Fatalf("hooks test evidence missing: %s", result.stdout)
			}
		})
	}
}

func TestResolveWave2UnknownActionWarnings(t *testing.T) {
	s := newSandbox(t)
	s.configure(func(c *config.Config) {
		c.Policy.Rules = []config.Rule{{ID: "obsolete", Decision: "deny", Match: config.Match{Operations: config.StringList{"issue.comment", "not.an.action", "issue.comment.*"}}}}
		h := s.hook("obsolete-hook", "pre")
		h.Match.Operations = config.StringList{"pr.comment", "graphql.*"}
		c.Hooks = []config.Hook{h}
	})
	for _, args := range [][]string{{"config", "validate"}, {"doctor", "--json"}} {
		result := s.management(nil, args...)
		result.code(t, 0)
		output := string(result.stdout)
		if strings.Count(output, "matches no known action") != 3 || !strings.Contains(output, "policy.rules[0]") || !strings.Contains(output, "hooks[0]") {
			t.Fatalf("warnings: %s", output)
		}
	}
}

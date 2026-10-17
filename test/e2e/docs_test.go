//go:build unix

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestManagementDocs(t *testing.T) {
	s := newSandbox(t)
	// Discovery must not depend on valid configuration or an installed gh.
	if err := os.WriteFile(s.cfg, []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	s.env["GHOK_REAL_GH"] = "/no-such-gh"
	text := s.management(nil, "docs")
	text.code(t, 0)
	if !bytes.Contains(text.stdout, []byte("  security/project-configuration-trust  Project configuration trust — ")) || len(text.stderr) != 0 {
		t.Fatalf("docs tree stdout=%s stderr=%s", text.stdout, text.stderr)
	}
	tree := s.management(nil, "docs", "--json")
	tree.code(t, 0)
	var sections []struct {
		ID, Title, Description, File string
		Depth                        int
	}
	decodeJSON(t, tree.stdout, &sections)
	ids := make(map[string]bool)
	for _, section := range sections {
		if section.ID == "" || section.Title == "" || ids[section.ID] || strings.Contains(section.ID, ".") {
			t.Fatalf("invalid or duplicate docs section: %+v", section)
		}
		ids[section.ID] = true
		if section.File != "README.md" && section.File != "skills/ghok/SKILL.md" && (!strings.HasPrefix(section.File, "docs/") || strings.Count(section.File, "/") != 1) {
			t.Fatalf("unexpected embedded file: %q", section.File)
		}
		if section.ID == "security/project-configuration-trust" && section.Depth != 1 {
			t.Fatalf("incorrect heading depth: %+v", section)
		}
	}
	for _, id := range []string{"readme", "security", "security/project-configuration-trust", "hook-protocol/parallel-hooks", "skill"} {
		if !ids[id] {
			t.Fatalf("missing docs id %q", id)
		}
	}
	section := s.management(nil, "docs", "security/project-configuration-trust")
	section.code(t, 0)
	if !bytes.HasPrefix(section.stdout, []byte("## Project configuration trust\n")) || !bytes.Contains(section.stdout, []byte("### Transform trust and failures\n")) || bytes.Contains(section.stdout, []byte("## Redaction rules and limits\n")) || len(section.stderr) != 0 {
		t.Fatalf("incorrect section extraction: stdout=%s stderr=%s", section.stdout, section.stderr)
	}
	all := s.management(nil, "docs", "--all")
	all.code(t, 0)
	for _, file := range []string{"README.md", "docs/architecture.md", "docs/compatibility.md", "docs/hook-protocol.md", "docs/normalization.md", "docs/security.md", "skills/ghok/SKILL.md"} {
		if !bytes.Contains(all.stdout, []byte("<!-- file: "+file+" -->\n")) {
			t.Fatalf("missing full-docs marker for %s", file)
		}
	}
	if bytes.Contains(all.stdout, []byte("<!-- file: docs/notes/")) || len(all.stderr) != 0 {
		t.Fatalf("full docs include notes or errors: %s", all.stderr)
	}
	s.absentRecord()
}

func TestManagementDocsSearchAndUnknown(t *testing.T) {
	s := newSandbox(t)
	result := s.management(nil, "docs", "search", "parallel hooks")
	result.code(t, 0)
	var matches []struct {
		ID, Title, Description, URL string
		Score                       float64
	}
	decodeJSON(t, result.stdout, &matches)
	if len(matches) == 0 || len(matches) > 5 || matches[0].ID != "hook-protocol/parallel-hooks" || matches[0].Title == "" || matches[0].Description == "" || matches[0].Score <= 0 {
		t.Fatalf("unexpected search matches: %+v", matches)
	}
	if matches[0].URL != "https://github.com/mkusaka/ghok/blob/main/docs/hook-protocol.md#parallel-hooks" || len(result.stderr) != 0 {
		t.Fatalf("unexpected search URL/stderr: %+v %s", matches, result.stderr)
	}
	empty := s.management(nil, "docs", "search", "zzzznonexistenttermzzzz")
	empty.code(t, 0)
	if strings.TrimSpace(string(empty.stdout)) != "[]" {
		t.Fatalf("no matches must be a JSON array: %s", empty.stdout)
	}
	unknown := s.management(nil, "docs", "security/project-configuration-trus")
	unknown.code(t, 1)
	if len(unknown.stdout) != 0 || !bytes.HasPrefix(unknown.stderr, []byte("ghok: unknown docs id \"security/project-configuration-trus\"")) || !bytes.Contains(unknown.stderr, []byte("security/project-configuration-trust")) {
		t.Fatalf("unknown id stdout=%q stderr=%q", unknown.stdout, unknown.stderr)
	}
	for _, args := range [][]string{
		{"docs", "search"}, {"docs", "search", "parallel", "hooks"},
		{"docs", "readme", "security"}, {"docs", "--all", "readme"},
		{"docs", "--all", "--json"},
	} {
		bad := s.management(nil, args...)
		bad.code(t, 2)
		if len(bad.stdout) != 0 || !bytes.HasPrefix(bad.stderr, []byte("ghok:")) {
			t.Fatalf("usage %q stdout=%q stderr=%q", args, bad.stdout, bad.stderr)
		}
	}
	s.absentRecord()
}

func TestManagementActions(t *testing.T) {
	s := newSandbox(t)
	text := s.management(nil, "actions", "pr.merge")
	text.code(t, 0)
	for _, expected := range []string{"pr.merge.request", "pr.merge.direct", "write", "gh pr merge", "PUT /repos/{owner}/{repo}/pulls/{pull_number}/merge", "mergePullRequest"} {
		if !bytes.Contains(text.stdout, []byte(expected)) {
			t.Fatalf("actions missing %q: %s", expected, text.stdout)
		}
	}
	result := s.management(nil, "actions", "pr.merge", "--json")
	result.code(t, 0)
	var families []struct {
		Family  string `json:"family"`
		Actions []struct {
			Action, Effect        string
			Native, REST, GraphQL []string
		} `json:"actions"`
	}
	decodeJSON(t, result.stdout, &families)
	found := map[string]bool{}
	for _, family := range families {
		if family.Family == "" {
			t.Fatalf("missing action family: %+v", family)
		}
		for _, action := range family.Actions {
			if !strings.HasPrefix(action.Action, "pr.merge") || (action.Effect != "write" && action.Effect != "read") {
				t.Fatalf("action prefix/effect mismatch: %+v", action)
			}
			found[action.Action] = true
			if action.Action == "pr.merge.direct" && (!slices.Contains(action.REST, "PUT /repos/{owner}/{repo}/pulls/{pull_number}/merge") || !slices.Contains(action.GraphQL, "mergePullRequest")) {
				t.Fatalf("missing direct merge routes: %+v", action)
			}
		}
	}
	if !found["pr.merge.request"] || !found["pr.merge.direct"] || !found["pr.merge.queue.enqueue"] {
		t.Fatalf("missing merge intents: %+v", families)
	}
	empty := s.management(nil, "actions", "no-such-action", "--json")
	empty.code(t, 0)
	var emptyArray []json.RawMessage
	decodeJSON(t, empty.stdout, &emptyArray)
	if emptyArray == nil || len(emptyArray) != 0 {
		t.Fatalf("no actions must be an empty array: %s", empty.stdout)
	}
	s.management(nil, "actions", "pr.merge", "extra").code(t, 2)
	s.absentRecord()
}

func TestDocsSkillSourceURL(t *testing.T) {
	s := newSandbox(t)
	result := s.management(nil, "docs", "skill")
	result.code(t, 0)
	// e2e builds use a non-release version, so the URL points at main.
	if !bytes.HasPrefix(result.stdout, []byte("<!-- Agent skill source for this ghok version: https://github.com/mkusaka/ghok/blob/main/skills/ghok/SKILL.md ")) {
		t.Fatalf("skill header: %.200s", result.stdout)
	}
	if bytes.Contains(s.management(nil, "docs", "readme").stdout, []byte("Agent skill source")) {
		t.Fatal("non-skill section got the skill header")
	}
}

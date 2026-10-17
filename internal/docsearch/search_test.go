package docsearch

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	ghok "github.com/mkusaka/ghok"
)

func TestTokenizer(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"wait-all GH_TOKEN", []string{"wait-all", "wait", "all", "gh_token", "gh", "token"}},
		{"mergePullRequest HTTPServer", []string{"merge", "pull", "request", "http", "server"}},
		{"hooks passes blocked running", []string{"hook", "pass", "block", "runn"}},
		{"worktrees clones references classes boxes branches", []string{"worktree", "clone", "reference", "class", "box", "branch"}},
		{"  READ, write... 123 / API!", []string{"read", "write", "123", "api"}},
		{"日本語", nil},
	} {
		if got := tokenize(tc.text); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("tokenize(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestSearchWeightsCodeAndLimits(t *testing.T) {
	index := loadFixture(t, fixture("# Root\n\n## Body\n```\nneedle\n```\n## Description\nNeedle.\n## Needle\n```\nplain\n```\n"))
	results := index.Search("needle", "dev", 10)
	if len(results) < 3 || results[0].ID != "readme/needle" {
		t.Fatalf("heading must outweigh body-only matches: %+v", results)
	}
	if !containsID(results, "readme/body") {
		t.Errorf("code block content must be searchable: %+v", results)
	}
	positions := make(map[string]int)
	for n, result := range results {
		positions[result.ID] = n
		if math.Abs(result.Score*1000-math.Round(result.Score*1000)) > 0.000001 {
			t.Errorf("unrounded score: %g", result.Score)
		}
	}
	if positions["readme/description"] >= positions["readme/body"] {
		t.Errorf("description must outweigh body-only matches: %+v", results)
	}
	for _, limit := range []int{1, 2, 3} {
		if got := index.Search("needle", "dev", limit); len(got) != limit {
			t.Errorf("limit %d: %+v", limit, got)
		}
	}
	for _, tc := range []struct {
		query string
		limit int
	}{{"", 5}, {"needle", 0}, {"needle", -1}, {"zzzzzzzzzzzz", 5}} {
		got := index.Search(tc.query, "dev", tc.limit)
		data, err := json.Marshal(got)
		if err != nil || string(data) != "[]" {
			t.Errorf("Search(%q, %d) JSON = %s, %v; want []", tc.query, tc.limit, data, err)
		}
	}
}

func TestSynonymsAndMisspellingSuggestions(t *testing.T) {
	index := loadFixture(t, fixture("# Root\n\n## Repository\nStore project source.\n## PR\nManage changes.\n## Review\nInspect code.\n## Parallel hooks\nRun concurrently.\n"))
	for _, tc := range []struct{ query, id string }{
		{"repo", "readme/repository"},
		{"pull request", "readme/pr"},
		{"approve", "readme/review"},
		{"concurrency", "readme/parallel-hooks"},
	} {
		results := index.Search(tc.query, "dev", 1)
		if len(results) != 1 || results[0].ID != tc.id {
			t.Errorf("Search(%q) = %+v, want first %s", tc.query, results, tc.id)
		}
	}
	ids := index.Suggest("paralel-hooks")
	if len(ids) == 0 || len(ids) > 3 || ids[0] != "readme/parallel-hooks" {
		t.Errorf("misspelling suggestions: %v", ids)
	}
}

func TestSearchTieOrderIsStable(t *testing.T) {
	index := loadFixture(t, fixture("# Root\n\n## Zulu\n```\nneedle\n```\n## Alpha\n```\nneedle\n```\n"))
	first := index.Search("needle", "dev", 10)
	var tied []Result
	for _, result := range first {
		if result.ID == "readme/alpha" || result.ID == "readme/zulu" {
			tied = append(tied, result)
		}
	}
	if len(tied) != 2 || tied[0].ID != "readme/alpha" || tied[0].Score != tied[1].Score {
		t.Fatalf("ties must sort by id, not source order: %+v", first)
	}
	if again := index.Search("needle", "dev", 10); !reflect.DeepEqual(first, again) {
		t.Errorf("search order changed: %+v versus %+v", first, again)
	}
}

func TestSearchDoesNotDuplicateSubsections(t *testing.T) {
	index := loadFixture(t, fixture("# Root\nIntroduction.\n## Needle\nSpecific content.\n"))
	results := index.Search("needle", "dev", 5)
	if len(results) != 1 || results[0].ID != "readme/needle" {
		t.Fatalf("subsection content must not be indexed again under its parent: %+v", results)
	}
}

func TestVersionURLsAndResultJSON(t *testing.T) {
	index := loadFixture(t, fixture("# Root\n\n## Needle!\nUseful prose.\n"))
	for _, tc := range []struct{ version, ref string }{
		{"dev", "main"}, {"", "main"}, {"main", "main"}, {"abc123", "main"},
		{"v1.2.3-4-gabcdef", "main"},
		{"v0.0.14+dirty", "main"},
		{"1.2.3", "v1.2.3"}, {"v1.2.3", "v1.2.3"},
		{"1.2.3-rc.1", "v1.2.3-rc.1"}, {"v1.2.3-beta.2+build.7", "v1.2.3-beta.2+build.7"},
		{"v1.2.3-dirty", "main"}, {"1.2", "main"}, {"v0.0.0-20261018000000-abcdef123456", "main"},
	} {
		results := index.Search("needle", tc.version, 1)
		want := "https://github.com/mkusaka/ghok/blob/" + tc.ref + "/README.md#needle"
		if len(results) != 1 || results[0].URL != want {
			t.Errorf("version %q results = %+v, want URL %s", tc.version, results, want)
		}
	}
	data, err := json.Marshal(index.Search("needle", "dev", 1))
	if err != nil {
		t.Fatal(err)
	}
	var fields []map[string]any
	if err = json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || len(fields[0]) != 5 || fields[0]["id"] != "readme/needle" || fields[0]["title"] != "Needle!" || fields[0]["description"] != "Useful prose." || fields[0]["url"] == nil || fields[0]["score"] == nil {
		t.Errorf("result JSON = %s", data)
	}
}

func TestEmbeddedCorpusSearch(t *testing.T) {
	index, err := Load(ghok.Docs)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, section := range index.Sections {
		if seen[section.ID] || strings.TrimSpace(section.Title) == "" {
			t.Errorf("duplicate ID or empty title: %+v", section)
		}
		seen[section.ID] = true
	}
	for _, tc := range []struct {
		query, id string
		limit     int
	}{
		{"parallel hooks", "hook-protocol/parallel-hooks", 1},
		{"GH_TOKEN gain", "hook-protocol/transform-environment-edits", 3},
		{"trust worktree", "security/project-configuration-trust", 3},
	} {
		results := index.Search(tc.query, "dev", tc.limit)
		if !containsID(results, tc.id) {
			t.Errorf("Search(%q) top %d = %+v, want %s", tc.query, tc.limit, results, tc.id)
		}
	}
	ids := index.Suggest("security/project-configuration-trus")
	found := false
	for _, id := range ids {
		found = found || id == "security/project-configuration-trust"
	}
	if !found {
		t.Errorf("unknown ID suggestions = %v, want security/project-configuration-trust", ids)
	}
}

func containsID(results []Result, id string) bool {
	for _, result := range results {
		if result.ID == id {
			return true
		}
	}
	return false
}

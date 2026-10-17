package docsearch

import (
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type Result struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	URL         string  `json:"url"`
	Score       float64 `json:"score"`
}

type document struct {
	terms  map[string]float64
	length float64
}

func (index *Index) addDocument(section Section, body string) {
	doc := document{terms: make(map[string]float64)}
	for _, field := range []struct {
		text   string
		weight float64
	}{{section.Title, 3}, {section.Description, 2}, {body, 1}} {
		for _, token := range tokenize(field.text) {
			doc.terms[token] += field.weight
			doc.length += field.weight
		}
	}
	for term := range doc.terms {
		index.frequency[term]++
	}
	index.average += doc.length
	index.documents = append(index.documents, doc)
}

// Search uses BM25 (k1=1.2, b=0.75) with weighted field frequencies.
// Each body stops at the next heading; Lookup still includes subsections.
func (index *Index) Search(query, version string, limit int) []Result {
	results := make([]Result, 0)
	if limit <= 0 || len(index.documents) == 0 {
		return results
	}
	terms := index.queryTerms(query)
	for n, doc := range index.documents {
		score := 0.0
		for _, term := range terms {
			tf := doc.terms[term]
			if tf == 0 {
				continue
			}
			df := float64(index.frequency[term])
			idf := math.Log(1 + (float64(len(index.documents))-df+0.5)/(df+0.5))
			score += idf * tf * 2.2 / (tf + 1.2*(0.25+0.75*doc.length/index.average))
		}
		if score > 0 {
			section := index.Sections[n]
			results = append(results, Result{
				ID: section.ID, Title: section.Title, Description: section.Description,
				URL:   SourceURL(version, section.File) + "#" + url.PathEscape(section.Anchor),
				Score: score,
			})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].ID < results[j].ID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	for n := range results {
		results[n].Score = math.Round(results[n].Score*1000) / 1000
	}
	return results
}

func (index *Index) Suggest(id string) []string {
	results := index.Search(id, "dev", 3)
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.ID)
	}
	return ids
}

var releaseVersion = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)
var developmentVersion = regexp.MustCompile(`(?:[+-]dirty|-[0-9]+-g[0-9a-f]+|-[0-9]{14}-[0-9a-f]{12})$`)

// SourceURL is file's GitHub URL at the tag matching version (main for dev builds).
func SourceURL(version, file string) string {
	return "https://github.com/mkusaka/ghok/blob/" + versionRef(version) + "/" + file
}

func versionRef(version string) string {
	if !releaseVersion.MatchString(version) || developmentVersion.MatchString(version) {
		return "main"
	}
	return "v" + strings.TrimPrefix(version, "v")
}

var tokenPattern = regexp.MustCompile(`[A-Za-z0-9]+(?:[-_][A-Za-z0-9]+)*`)

func tokenize(text string) []string {
	var tokens []string
	for _, word := range tokenPattern.FindAllString(text, -1) {
		if strings.ContainsAny(word, "-_") {
			tokens = append(tokens, stem(strings.ToLower(word)))
		}
		for _, part := range strings.FieldsFunc(word, func(r rune) bool { return r == '-' || r == '_' }) {
			start := 0
			for n := 1; n < len(part); n++ {
				if unicode.IsUpper(rune(part[n])) && (unicode.IsLower(rune(part[n-1])) || (n+1 < len(part) && unicode.IsLower(rune(part[n+1])) && unicode.IsUpper(rune(part[n-1])))) {
					tokens = append(tokens, stem(strings.ToLower(part[start:n])))
					start = n
				}
			}
			tokens = append(tokens, stem(strings.ToLower(part[start:])))
		}
	}
	return tokens
}

func stem(term string) string {
	for _, suffix := range []string{"ing", "es", "ed", "s"} {
		// Only sibilant plurals add "es"; ordinary plurals retain a final e
		// (worktrees -> worktree, references -> reference).
		if suffix == "es" && !strings.HasSuffix(term, "sses") && !strings.HasSuffix(term, "xes") && !strings.HasSuffix(term, "zes") && !strings.HasSuffix(term, "ches") && !strings.HasSuffix(term, "shes") {
			continue
		}
		if suffix == "s" && strings.HasSuffix(term, "ss") {
			continue
		}
		if len(term)-len(suffix) >= 3 && strings.HasSuffix(term, suffix) {
			return strings.TrimSuffix(term, suffix)
		}
	}
	return term
}

// Synonyms expand only queries, never the indexed corpus.
var synonyms = [][]string{
	{"pr", "pull request"},
	{"repo", "repository"},
	{"approve", "review"},
	{"parallel", "concurrency", "concurrent"},
	{"env", "environment"},
	{"config", "configuration"},
	{"auth", "authentication"},
	{"deny", "block"},
	{"token", "credential"},
	{"cli", "command"},
}

func (index *Index) queryTerms(query string) []string {
	terms := make(map[string]bool)
	for _, term := range tokenize(query) {
		terms[term] = true
	}
	for _, group := range synonyms {
		match := false
		for _, phrase := range group {
			words := tokenize(phrase)
			all := true
			for _, word := range words {
				all = all && terms[word]
			}
			match = match || all
		}
		if match {
			for _, phrase := range group {
				for _, word := range tokenize(phrase) {
					terms[word] = true
				}
			}
		}
	}
	result := make([]string, 0, len(terms))
	for term := range terms {
		result = append(result, term)
	}
	sort.Strings(result)
	return result
}

package rest

import (
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

//go:generate go run ./gen -ref ad351b19702b757bfd03a8ee85bcafe717878331 -date 2026-10-13

type pathParameter struct {
	name, types string
}

type routeSegment struct {
	literal string
	pathParameter
}

type route struct {
	method, path, operationID string
	parameters                []pathParameter
	tags                      []string
	deprecated                bool
	family, action            string
	effect                    model.Effect
	segments                  []routeSegment
	literals                  int
	tail                      bool
}

type routeKey struct {
	method, first string
	count         int
}

type routeIndex struct {
	exact map[routeKey][]*route
	tails map[routeKey][]*route
}

var routes = compileRoutes(generatedRoutes)

// Compile once; neither schema metadata nor parameter spelling is parsed in
// the hot path. Candidates are ordered so a literal route wins over captures.
func compileRoutes(rs []route) routeIndex {
	index := routeIndex{exact: make(map[routeKey][]*route), tails: make(map[routeKey][]*route)}
	for i := range rs {
		r := &rs[i]
		r.family = "rest." + strings.SplitN(r.operationID, "/", 2)[0]
		r.action = "rest." + strings.ReplaceAll(r.operationID, "/", ".")
		switch r.method {
		case "GET", "HEAD":
			r.effect = model.EffectRead
		case "POST", "PUT", "PATCH", "DELETE":
			r.effect = model.EffectWrite
		default:
			r.effect = model.EffectUnknown
		}
		if override, ok := routeOverrides[r.operationID]; ok {
			r.family, r.action, r.effect = override.Family, override.Action, override.Effect
		}
		if r.path != "/" {
			for _, part := range strings.Split(strings.TrimPrefix(r.path, "/"), "/") {
				segment := routeSegment{literal: part}
				if strings.HasPrefix(part, "{") {
					segment.literal = ""
					name := strings.Trim(part, "{}")
					for _, p := range r.parameters {
						if p.name == name {
							segment.pathParameter = p
							break
						}
					}
				} else {
					r.literals++
				}
				r.segments = append(r.segments, segment)
			}
		}
		// Preserve documented terminal slash-bearing contents/git-ref captures.
		// Branches elsewhere can include encoded slashes, not extra path segments.
		r.tail = strings.HasPrefix(r.operationID, "git/") && len(r.segments) > 0 && r.segments[len(r.segments)-1].name == "ref" ||
			r.operationID == "repos/get-content" || r.operationID == "repos/create-or-update-file-contents" || r.operationID == "repos/delete-file"
		key := routeKey{method: r.method, count: len(r.segments)}
		if len(r.segments) > 0 {
			key.first = r.segments[0].literal
		}
		if r.tail {
			key.count = 0
			index.tails[key] = append(index.tails[key], r)
		} else {
			index.exact[key] = append(index.exact[key], r)
		}
	}
	for _, bucket := range []map[routeKey][]*route{index.exact, index.tails} {
		for _, candidates := range bucket {
			slices.SortStableFunc(candidates, func(a, b *route) int { return b.literals - a.literals })
		}
	}
	return index
}

func matchRoute(method string, segments []string) (*route, map[string]string) {
	key := routeKey{method: method, count: len(segments)}
	if len(segments) > 0 {
		key.first = segments[0]
	}
	var best *route
	for _, r := range routes.exact[key] {
		if routeMatches(r, segments) {
			best = r
			break
		}
	}
	key.count = 0
	for _, r := range routes.tails[key] {
		if (best == nil || r.literals > best.literals) && routeMatches(r, segments) {
			best = r
			break
		}
	}
	if best == nil {
		return nil, nil
	}
	captures := make(map[string]string, len(best.parameters))
	for i, pattern := range best.segments {
		if pattern.name == "" {
			continue
		}
		if best.tail && i == len(best.segments)-1 {
			captures[pattern.name] = strings.Join(segments[i:], "/")
		} else {
			captures[pattern.name] = segments[i]
		}
	}
	return best, captures
}

func routeMatches(r *route, segments []string) bool {
	minimum := len(r.segments)
	// GitHub documents omitting the contents path to list the repository root.
	if r.operationID == "repos/get-content" {
		minimum--
	}
	if (!r.tail && len(segments) != minimum) || (r.tail && len(segments) < minimum) {
		return false
	}
	for i, pattern := range r.segments {
		if r.tail && i == len(r.segments)-1 {
			return true
		}
		if pattern.name == "" {
			if pattern.literal != segments[i] {
				return false
			}
		} else if !validParameter(pattern.pathParameter, segments[i]) {
			return false
		}
	}
	return true
}

func validParameter(p pathParameter, value string) bool {
	if !validCapture(p.name, value) {
		return false
	}
	if p.name == "workflow_id" {
		return positiveDecimal(value) || strings.HasSuffix(value, ".yml") || strings.HasSuffix(value, ".yaml")
	}
	if p.types == "integer" {
		return positiveDecimal(value)
	}
	return p.types == "string" || p.types == "integer|string"
}

func positiveDecimal(value string) bool {
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(value)
	return err == nil && n > 0
}

func validCapture(name, value string) bool {
	if value == "" {
		return false
	}
	switch name {
	case "branch", "environment_name", "name", "tag", "ref", "path", "commit_sha", "basehead":
		return !strings.ContainsAny(value, "{}") || (name == "branch" && value == "{branch}")
	}
	if strings.ContainsRune(value, '/') {
		return false
	}
	if !strings.ContainsAny(value, "{}:") {
		return true
	}
	if hasPlaceholder(value) {
		return (name == "owner" && (value == "{owner}" || value == ":owner")) || (name == "repo" && (value == "{repo}" || value == ":repo"))
	}
	return !strings.ContainsAny(value, "{}")
}

// KnownActionPattern checks emitted OpenAPI-backed default action names.
func KnownActionPattern(glob string) bool {
	for _, route := range generatedRoutes {
		if _, curated := routeOverrides[route.operationID]; curated {
			continue
		}
		if match, _ := path.Match(glob, route.action); match {
			return true
		}
	}
	return false
}

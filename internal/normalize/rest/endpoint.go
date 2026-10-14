package rest

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/mkusaka/ghok/internal/model"
)

type endpoint struct {
	host, path string
	segments   []string
	query      url.Values
	unresolved []model.Unresolved
	opaque     bool
}

// This is gh's placeholder syntax, including its legacy :owner form.
var placeholderRE = regexp.MustCompile(`(\:(owner|repo|branch)\b|\{[a-z]+\})`)

func hasPlaceholder(s string) bool {
	for _, match := range placeholderRE.FindAllString(s, -1) {
		switch match {
		case "{owner}", "{repo}", "{branch}", ":owner", ":repo", ":branch":
			return true
		}
	}
	return false
}

func expandPlaceholders(inv model.Invocation, value, field string) (string, []model.Unresolved) {
	var unresolved []model.Unresolved
	parts := strings.Split(inv.Env["GH_REPO"], "/")
	var owner, repo string
	if len(parts) == 2 || len(parts) == 3 {
		owner, repo = parts[len(parts)-2], parts[len(parts)-1]
		if owner == "" || repo == "" || strings.ContainsAny(owner+repo, "{} :?#\\") {
			owner, repo = "", ""
		}
	}
	value = placeholderRE.ReplaceAllStringFunc(value, func(p string) string {
		name := strings.Trim(p, "{}:")
		switch name {
		case "owner":
			if owner != "" {
				return owner
			}
		case "repo":
			if repo != "" {
				return repo
			}
		case "branch":
		default:
			return p // gh leaves unknown brace names unchanged.
		}
		source, reason := "git", "placeholder {"+name+"} requires local git context"
		if name != "branch" && inv.Env["GH_REPO"] != "" {
			source, reason = "env", "GH_REPO is not a valid [HOST/]OWNER/REPO"
		}
		unresolved = appendUnresolved(unresolved, model.Unresolved{Field: field, Reason: reason, Source: source})
		return p
	})
	return value, unresolved
}

func appendUnresolved(us []model.Unresolved, u model.Unresolved) []model.Unresolved {
	for _, existing := range us {
		if existing == u {
			return us
		}
	}
	return append(us, u)
}

func parseEndpoint(inv model.Invocation, req model.APIRequest) (endpoint, error) {
	var ep endpoint
	raw, unresolved := expandPlaceholders(inv, req.Endpoint, "endpoint")
	ep.unresolved = unresolved
	if raw == "" {
		return ep, fmt.Errorf("empty REST endpoint")
	}
	if strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return ep, fmt.Errorf("control character in REST endpoint")
	}
	var path, query string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Fragment != "" {
			return ep, fmt.Errorf("invalid absolute REST endpoint")
		}
		ep.host = strings.ToLower(u.Host)
		path, query = u.EscapedPath(), u.RawQuery
		switch {
		case strings.EqualFold(u.Host, "api.github.com"), strings.EqualFold(u.Host, "uploads.github.com"):
			ep.host = "github.com"
		case strings.HasPrefix(ep.host, "api.") && strings.HasSuffix(ep.host, ".ghe.com"):
			ep.host = strings.TrimPrefix(ep.host, "api.")
		case strings.HasPrefix(path, "/api/v3/"):
			path = strings.TrimPrefix(path, "/api/v3")
		default:
			ep.opaque = true // arbitrary absolute URLs need not be GitHub APIs.
		}
	} else {
		if strings.ContainsAny(raw, "#\\") {
			return ep, fmt.Errorf("fragment or backslash in relative REST endpoint")
		}
		ep.host = req.Hostname
		if ep.host == "" {
			ep.host = inv.Env["GH_HOST"]
		}
		if ep.host == "" {
			ep.unresolved = append(ep.unresolved, model.Unresolved{Field: "target.host", Reason: "request host depends on gh configuration", Source: "git/config"})
		} else {
			if strings.TrimSpace(ep.host) == "" || strings.ContainsAny(ep.host, "/:?#\\ \t\r\n") {
				return ep, fmt.Errorf("invalid REST hostname")
			}
			ep.host = strings.ToLower(ep.host)
		}
		path, query, _ = strings.Cut(raw, "?")
		// gh strips exactly one leading slash and adds RESTPrefix. In
		// particular, a relative /api/v3 prefix must NOT be stripped.
		path = "/" + strings.TrimPrefix(path, "/")
	}
	var err error
	ep.query, err = url.ParseQuery(query)
	if err != nil {
		return ep, fmt.Errorf("invalid REST query encoding")
	}
	if path == "/" || (path == "" && strings.Contains(raw, "://")) {
		ep.path = "/"
		return ep, nil
	}
	if !strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return ep, fmt.Errorf("empty or trailing-slash REST path is not normalized")
	}
	rawSegments := strings.Split(path[1:], "/")
	ep.segments = make([]string, len(rawSegments))
	for i, s := range rawSegments {
		v, err := url.PathUnescape(s)
		if err != nil || v == "" || v == "." || v == ".." || strings.ContainsRune(v, '\\') || strings.IndexFunc(v, unicode.IsControl) >= 0 {
			return ep, fmt.Errorf("ambiguous or invalid REST path segment")
		}
		// Encoded slashes remain within a single segment. Reject traversal
		// inside such segments too; don't silently path.Clean user input.
		for _, part := range strings.Split(v, "/") {
			if part == "." || part == ".." || part == "" {
				return ep, fmt.Errorf("ambiguous encoded REST path segment")
			}
		}
		ep.segments[i] = v
	}
	ep.path = "/" + strings.Join(ep.segments, "/")
	return ep, nil
}

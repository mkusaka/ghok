package policy

import "strings"

// Token kinds for config.Match.Tokens.
const (
	TokenApp     = "app"     // GitHub App installation token (ghs_): gain, GitHub Actions GITHUB_TOKEN
	TokenUser    = "user"    // acts as a user: classic/fine-grained PAT, OAuth, App user-to-server
	TokenUnknown = "unknown" // set, but not a recognised GitHub token format
	TokenNone    = "none"    // no env token: gh uses its stored login (a user)
)

// TokenKind classifies the token gh would read from the environment, using
// gh's precedence (GH_TOKEN, then GITHUB_TOKEN) for github.com. Only the
// documented prefix is inspected; the value is never returned or stored.
// GHES enterprise tokens (GH_ENTERPRISE_TOKEN) are not considered.
func TokenKind(env map[string]string) string {
	token := env["GH_TOKEN"]
	if token == "" {
		token = env["GITHUB_TOKEN"]
	}
	switch {
	case token == "":
		return TokenNone
	case strings.HasPrefix(token, "ghs_"):
		return TokenApp
	case strings.HasPrefix(token, "ghp_"), strings.HasPrefix(token, "gho_"),
		strings.HasPrefix(token, "ghu_"), strings.HasPrefix(token, "github_pat_"):
		return TokenUser
	default:
		return TokenUnknown
	}
}

package policy

import (
	"testing"

	"github.com/mkusaka/ghok/internal/config"
)

func TestTokenKind(t *testing.T) {
	fake := "0123456789abcdefghijklmnopqrstuvwxyzAB" // not a real credential
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{nil, TokenNone},
		{map[string]string{"GH_TOKEN": "ghs" + "_" + fake}, TokenApp},
		{map[string]string{"GITHUB_TOKEN": "ghs" + "_" + fake}, TokenApp},
		{map[string]string{"GH_TOKEN": "ghp" + "_" + fake, "GITHUB_TOKEN": "ghs" + "_" + fake}, TokenUser}, // GH_TOKEN wins
		{map[string]string{"GH_TOKEN": "gho" + "_" + fake}, TokenUser},
		{map[string]string{"GH_TOKEN": "ghu" + "_" + fake}, TokenUser},
		{map[string]string{"GH_TOKEN": "github" + "_pat_" + fake}, TokenUser},
		{map[string]string{"GH_TOKEN": "something-else"}, TokenUnknown},
		{map[string]string{"GH_ENTERPRISE_TOKEN": "ghs" + "_" + fake}, TokenNone},
	} {
		if got := TokenKind(tc.env); got != tc.want {
			t.Errorf("%v: got %s, want %s", tc.env, got, tc.want)
		}
	}
}

func TestMatchToken(t *testing.T) {
	m := config.Match{Tokens: config.StringList{TokenApp}}
	if !invocationMatches(m, Subject{Token: TokenApp}) {
		t.Error("app token should match")
	}
	if invocationMatches(m, Subject{Token: TokenUser}) || invocationMatches(m, Subject{}) {
		t.Error("user/none must not match token: app")
	}
	if !invocationMatches(config.Match{Tokens: config.StringList{TokenNone}}, Subject{}) {
		t.Error(`empty Subject.Token counts as "none"`)
	}
}

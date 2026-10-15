package normalize

import "testing"

func TestNeedsStdin(t *testing.T) {
	for _, tt := range []struct {
		name string
		argv []string
		want string
	}{
		{name: "API input", argv: []string{"api", "repos/o/r/issues", "--input", "-"}, want: "api"},
		{name: "API input equals", argv: []string{"api", "graphql", "--input=-"}, want: "api"},
		{name: "API typed field", argv: []string{"api", "graphql", "-F", "query=@-"}, want: "api"},
		{name: "API typed field attached", argv: []string{"api", "graphql", "-Fquery=@-"}, want: "api"},
		{name: "API bundled field", argv: []string{"api", "graphql", "-iFquery=@-"}, want: "api"},
		{name: "API long field", argv: []string{"api", "repos/o/r/issues", "--field=body=@-"}, want: "api"},
		{name: "API flags before terminator", argv: []string{"api", "--input=-", "--", "graphql"}, want: "api"},
		{name: "API raw field literal", argv: []string{"api", "graphql", "-f", "query=@-"}},
		{name: "API bundled raw field literal", argv: []string{"api", "graphql", "-ifquery=@-"}},
		{name: "API long raw field literal", argv: []string{"api", "graphql", "--raw-field=body=@-"}},
		{name: "API typed filename", argv: []string{"api", "graphql", "-Fquery=@query.graphql"}},
		{name: "API input filename", argv: []string{"api", "graphql", "--input=query.json"}},
		{name: "API header literal", argv: []string{"api", "graphql", "-H", "body=@-"}},
		{name: "API replaced input", argv: []string{"api", "graphql", "--input=-", "--input=file.json"}},
		{name: "API missing endpoint", argv: []string{"api", "--input=-"}},
		{name: "API missing value", argv: []string{"api", "graphql", "--input=-", "-F"}},
		{name: "API unknown flag", argv: []string{"api", "graphql", "--input=-", "--not-a-flag"}},
		{name: "API help", argv: []string{"api", "graphql", "--input=-", "--help"}},
		{name: "API after terminator", argv: []string{"api", "graphql", "--", "--input", "-"}},
		{name: "PR create", argv: []string{"pr", "create", "--body-file", "-"}, want: "body"},
		{name: "issue create", argv: []string{"issue", "create", "--body-file=-"}, want: "body"},
		{name: "PR edit", argv: []string{"pr", "edit", "1", "-F", "-"}, want: "body"},
		{name: "issue edit", argv: []string{"issue", "edit", "1", "-F-"}, want: "body"},
		{name: "PR comment", argv: []string{"pr", "comment", "1", "--body-file", "-"}, want: "body"},
		{name: "issue comment", argv: []string{"issue", "comment", "1", "--body-file=-"}, want: "body"},
		{name: "PR review", argv: []string{"pr", "review", "1", "-F-"}, want: "body"},
		{name: "PR merge", argv: []string{"pr", "merge", "1", "-F-"}, want: "body"},
		{name: "native bundled flags", argv: []string{"pr", "create", "-dF-"}, want: "body"},
		{name: "native bundled review", argv: []string{"pr", "review", "1", "-aF", "-"}, want: "body"},
		{name: "native root repo flag", argv: []string{"-R", "o/r", "pr", "comment", "1", "-F-"}, want: "body"},
		{name: "native command alias", argv: []string{"pr", "new", "-F-"}, want: "body"},
		{name: "release create", argv: []string{"release", "create", "v1", "--notes-file", "-"}, want: "notes"},
		{name: "release edit", argv: []string{"release", "edit", "v1", "-F-"}, want: "notes"},
		{name: "inline body overrides file", argv: []string{"pr", "comment", "1", "--body=text", "-F-"}},
		{name: "empty inline body overrides file", argv: []string{"pr", "edit", "1", "-F-", "--body="}},
		{name: "inline notes override file", argv: []string{"release", "edit", "v1", "--notes=", "-F-"}},
		{name: "editor create", argv: []string{"issue", "create", "--editor", "-F-"}},
		{name: "web create", argv: []string{"pr", "create", "--web", "-F-"}},
		{name: "editor comment", argv: []string{"pr", "comment", "1", "--editor", "-F-"}},
		{name: "web comment", argv: []string{"issue", "comment", "1", "--web", "-F-"}},
		{name: "delete last comment", argv: []string{"pr", "comment", "1", "--delete-last", "-F-"}},
		{name: "disabled editor", argv: []string{"pr", "comment", "1", "--editor=false", "-F-"}, want: "body"},
		{name: "disabled delete last", argv: []string{"issue", "comment", "1", "--delete-last=false", "-F-"}, want: "body"},
		{name: "explicit body overrides fill", argv: []string{"pr", "create", "--fill", "-F-"}, want: "body"},
		{name: "generated release notes", argv: []string{"release", "create", "v1", "--generate-notes", "-F-"}},
		{name: "release notes from tag", argv: []string{"release", "create", "v1", "--notes-from-tag", "-F-"}},
		{name: "native file", argv: []string{"pr", "comment", "1", "-Fbody.md"}},
		{name: "native replaced file", argv: []string{"pr", "comment", "1", "-F-", "--body-file=body.md"}},
		{name: "native inline dash", argv: []string{"pr", "comment", "1", "--body=-"}},
		{name: "native inline flag literal", argv: []string{"pr", "comment", "1", "--body", "--body-file=-"}},
		{name: "native terminator", argv: []string{"pr", "comment", "1", "--", "--body-file", "-"}},
		{name: "native help", argv: []string{"pr", "comment", "1", "-F-", "--help"}},
		{name: "native short help", argv: []string{"pr", "comment", "1", "-F-", "-h"}},
		{name: "native parse error", argv: []string{"pr", "comment", "1", "-F-", "--editor=invalid"}},
		{name: "native unknown flag", argv: []string{"pr", "comment", "1", "-F-", "--not-a-flag"}},
		{name: "read command", argv: []string{"pr", "view", "1", "--body-file=-"}},
		{name: "unknown command", argv: []string{"future", "--body-file=-"}},
		{name: "extension forwarding", argv: []string{"extension", "exec", "custom", "--body-file=-"}},
		{name: "no input", argv: []string{"pr", "comment", "1"}},
		{name: "empty argv"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			needed, which := NeedsStdin(tt.argv)
			if needed != (tt.want != "") || which != tt.want {
				t.Fatalf("NeedsStdin(%q) = (%t, %q), want (%t, %q)", tt.argv, needed, which, tt.want != "", tt.want)
			}
		})
	}
}

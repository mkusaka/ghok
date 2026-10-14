package apiargs

import (
	"reflect"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
)

func TestParseMethod(t *testing.T) {
	cases := []struct {
		args   []string
		method string
		ep     string
	}{
		{[]string{"repos/o/r"}, "GET", "repos/o/r"},
		{[]string{"repos/o/r/issues", "-f", "title=x"}, "POST", "repos/o/r/issues"},
		{[]string{"-XPUT", "repos/o/r/pulls/1/merge"}, "PUT", "repos/o/r/pulls/1/merge"},
		{[]string{"--method=delete", "repos/o/r"}, "DELETE", "repos/o/r"},
		{[]string{"-X", "GET", "search/issues", "-f", "q=x"}, "GET", "search/issues"},
		{[]string{"--input", "body.json", "repos/o/r/issues"}, "POST", "repos/o/r/issues"},
		{[]string{"-iX", "PATCH", "repos/o/r"}, "PATCH", "repos/o/r"},
		{[]string{"--", "-weird"}, "GET", "-weird"},
		{[]string{"graphql", "-F", "query=@q.graphql"}, "POST", "graphql"},
	}
	for _, c := range cases {
		req, err := Parse(c.args)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if req.Method != c.method || req.Endpoint != c.ep {
			t.Errorf("%v: got %s %s", c.args, req.Method, req.Endpoint)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, a := range [][]string{{}, {"a", "b"}, {"--nope", "x"}, {"-X"}, {"-Z", "x"}, {"--paginate=maybe", "x"}} {
		if _, err := Parse(a); err == nil {
			t.Errorf("%v: expected error", a)
		}
	}
	if _, err := Parse([]string{"--help"}); err != ErrHelp {
		t.Errorf("want ErrHelp, got %v", err)
	}
}

func TestBuildParams(t *testing.T) {
	req, err := Parse([]string{"x", "-f", "a=1", "-F", "n=2", "-F", "b=true", "-F", "z=null",
		"-F", "p[][name]=e", "-F", "p[][vals][]=s", "-F", "p[][vals][]=t", "-f", "empty[]", "-F", "q=@-", "-F", "f=@file"})
	if err != nil {
		t.Fatal(err)
	}
	inv := model.Invocation{
		Stdin:    model.StdinData{Status: model.StdinCaptured, Data: []byte("STDIN")},
		ReadFile: func(p string) ([]byte, error) { return []byte("FILE:" + p), nil },
	}
	got, unresolved, err := BuildParams(req, inv)
	if err != nil || len(unresolved) != 0 {
		t.Fatal(err, unresolved)
	}
	want := map[string]any{
		"a": "1", "n": 2, "b": true, "z": nil, "empty": []any{}, "q": "STDIN", "f": "FILE:file",
		"p": []any{map[string]any{"name": "e", "vals": []any{"s", "t"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
	if !NeedsStdin(req) {
		t.Error("NeedsStdin")
	}
	_, unresolved, _ = BuildParams(req, model.Invocation{})
	if len(unresolved) != 2 {
		t.Errorf("unresolved %v", unresolved)
	}
}

func TestIsGraphQL(t *testing.T) {
	for ep, want := range map[string]bool{
		"graphql": true, "/graphql": true, "graphql/": true, "https://api.github.com/graphql": true,
		"https://ghe.example.com/api/graphql?x=1": true, "repos/o/r/graphql": false, "https://api.github.com/repos/o/graphql": false,
	} {
		if got := IsGraphQL(model.APIRequest{Endpoint: ep}); got != want {
			t.Errorf("%s: got %v", ep, got)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add("-X\x00PUT\x00repos/o/r\x00-f\x00a[b][]=c")
	f.Fuzz(func(t *testing.T, s string) {
		args := splitNUL(s)
		req, err := Parse(args)
		if err == nil {
			_, _, _ = BuildParams(req, model.Invocation{})
		}
	})
}

func splitNUL(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

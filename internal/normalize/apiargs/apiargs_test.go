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

func TestParsePflagSemantics(t *testing.T) {
	t.Run("preview CSV and repeatable literal fields", func(t *testing.T) {
		req, err := Parse([]string{"repos/o/r", "-p", `"a,b",c`, "--preview=d", "--preview=",
			"-H", "Accept: a,b", "-f", "body=a,b", "-F", "labels[]=c,d"})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(req.Previews, []string{"a,b", "c", "d"}) {
			t.Fatalf("preview CSV: %#v", req.Previews)
		}
		if !reflect.DeepEqual(req.Headers, []string{"Accept: a,b"}) {
			t.Fatalf("headers must not split commas: %#v", req.Headers)
		}
		params, _, err := BuildParams(req, model.Invocation{})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(params, map[string]any{"body": "a,b", "labels": []any{"c,d"}}) {
			t.Fatalf("field arrays must not split commas: %#v", params)
		}
	})
	t.Run("duration validation", func(t *testing.T) {
		for _, value := range []string{"bogus", "60", "999999999999999999999h"} {
			if _, err := Parse([]string{"repos/o/r", "--cache", value}); err == nil {
				t.Fatalf("invalid cache %q accepted", value)
			}
		}
		req, err := Parse([]string{"repos/o/r", "--cache", "60m"})
		if err != nil || req.Cache != "1h0m0s" {
			t.Fatalf("cache duration: %q, %v", req.Cache, err)
		}
	})
	t.Run("CSV errors precede help", func(t *testing.T) {
		if _, err := Parse([]string{"--preview", `"unterminated`, "--help"}); err == nil || err == ErrHelp {
			t.Fatalf("malformed CSV must be an error, got %v", err)
		}
	})
	t.Run("help shorthand and option boundary", func(t *testing.T) {
		if _, err := Parse([]string{"-ih"}); err != ErrHelp {
			t.Fatalf("bundled help shorthand: %v", err)
		}
		req, err := Parse([]string{"--", "--help"})
		if err != nil || req.Endpoint != "--help" {
			t.Fatalf("terminator consumed as help: %+v, %v", req, err)
		}
		req, err = Parse([]string{"repos/o/r", "-q", "--help"})
		if err != nil || req.JQ != "--help" {
			t.Fatalf("value consumed as help: %+v, %v", req, err)
		}
	})
	t.Run("explicit method and boolean shorthand", func(t *testing.T) {
		req, err := Parse([]string{"-i=false", "repos/o/r", "-f", "a=b", "--method="})
		if err != nil || !req.MethodExplicit || req.Method != "" || req.Include {
			t.Fatalf("explicit empty method or false include lost: %+v, %v", req, err)
		}
	})
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

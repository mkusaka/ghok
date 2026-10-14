package redact_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
	"github.com/mkusaka/ghok/internal/redact"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

const replacement = "[REDACTED]"

func TestStringPatterns(t *testing.T) {
	ghToken := "ghp_" + strings.Repeat("a", 36)
	r := redact.New(map[string]string{"CUSTOM_SECRET": "learned-value", "GH_TOKEN": "tiny"})
	tests := []struct{ name, input, want string }{
		{"learned anywhere", "prelearned-valuepost", "pre" + replacement + "post"},
		{"short github env", "tiny", replacement},
		{"github classic", "token=" + ghToken, "token=" + replacement},
		{"github oauth", "gho_" + strings.Repeat("b", 36), replacement},
		{"github user", "ghu_" + strings.Repeat("c", 36), replacement},
		{"github server", "ghs_" + strings.Repeat("d", 36), replacement},
		{"github refresh", "ghr_" + strings.Repeat("e", 36), replacement},
		{"fine grained", "github_pat_11_" + strings.Repeat("a", 70), replacement},
		{"v1 token", "v1." + strings.Repeat("a", 40), replacement},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9" + ".eyJzdWIiOiJmYWtlIn0.signature_fake", replacement},
		{"bearer", "Bearer short", "Bearer " + replacement},
		{"token authorization", "TOKEN short", "TOKEN " + replacement},
		{"basic", "Basic dXNlcjpwYXNz", "Basic " + replacement},
		{"authorization header", "Authorization: unusual short value", "Authorization: " + replacement},
		{"proxy authorization", "Proxy-Authorization: Basic short", "Proxy-Authorization: " + replacement},
		{"url auth", "visit https://user:p%40ss@host/path", "visit https://user:" + replacement + "@host/path"},
		{"private key", "before\n-----BEGIN RSA PRIVATE " + "KEY-----\nnot-a-key\n-----END RSA PRIVATE KEY-----\nafter", "before\n" + replacement + "\nafter"},
		{"private key unfinished", "-----BEGIN OPENSSH PRIVATE " + "KEY-----\nnot-a-key", replacement},
		{"ordinary prose", "keyboard tokenizer monkey key tokenization", "keyboard tokenizer monkey key tokenization"},
		{"ordinary URL", "https://user@host/path", "https://user@host/path"},
		{"short pattern", "ghp_short", "ghp_short"},
		{"empty", "", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := r.String(test.input); got != test.want {
				t.Fatalf("String(%q) = %q; want %q", test.input, got, test.want)
			}
			if got := r.String(test.want); got != test.want {
				t.Fatalf("redaction is not idempotent: %q -> %q", test.want, got)
			}
		})
	}
}

func TestEnvironmentLearning(t *testing.T) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "SERVICE_TOKEN", "MY_SECRET", "APP_PASSWORD", "APP_PASSWD", "SERVICE_API_KEY", "SSH_PRIVATE_KEY", "CREDENTIALS"} {
		t.Run(name, func(t *testing.T) {
			env := map[string]string{name: "fake-credential-value"}
			r := redact.New(env)
			env[name] = "changed-environment"
			if got := r.String("fake-credential-value"); got != replacement {
				t.Fatalf("secret not learned: %q", got)
			}
		})
	}
	r := redact.New(map[string]string{"APP_TOKEN": "short", "ORDINARY": "ordinary-long", "EMPTY_SECRET": ""})
	if got := r.String("short ordinary-long"); got != "short ordinary-long" {
		t.Fatalf("unexpected environment learning: %q", got)
	}
	var zero redact.Redactor
	if got := zero.String("Bearer fake"); got != "Bearer "+replacement {
		t.Fatalf("zero-value redactor: %q", got)
	}
}

func TestLearnedOverlapsAndMarkerBoundaries(t *testing.T) {
	tests := []struct {
		secrets []string
		input   string
	}{
		{[]string{"abcdefgh", "cdefghij"}, "abcdefghijabcdefgh"},
		{[]string{"abcdefgh", "x[REDACTED]y"}, "xabcdefghy"},
		{[]string{"[REDACTED]"}, "[REDACTED] ghp_" + strings.Repeat("a", 36)},
		{[]string{"REDACTED"}, "REDACTED Bearer secret"},
		{[]string{"aa"}, "aaaaaaa"},
		{[]string{"x", "y"}, "xxyyBearer xxx"},
	}
	for _, test := range tests {
		env := make(map[string]string)
		for i, value := range test.secrets {
			if i == 0 {
				env["GH_TOKEN"] = value
			} else {
				env["GITHUB_TOKEN"] = value
			}
		}
		got := redact.New(env).String(test.input)
		for _, value := range test.secrets {
			if strings.Contains(got, value) {
				t.Fatalf("output %q contains learned value %q", got, value)
			}
		}
	}
}

func TestArgvContextAndAttachedForms(t *testing.T) {
	tests := []struct {
		name        string
		input, want []string
	}{
		{"header separate", []string{"api", "user", "-H", "Authorization: short"}, nil},
		{"header attached", []string{"api", "user", "-HAuthorization: short"}, nil},
		{"header attached equals", []string{"api", "user", "-H=Authorization: short"}, nil},
		{"header long equals", []string{"api", "user", "--header=Authorization: short"}, nil},
		{"header bool bundle", []string{"api", "user", "-iHAuthorization: short"}, nil},
		{"ordinary header", []string{"api", "user", "-H", "Accept: application/json"}, []string{"api", "user", "-H", "Accept: application/json"}},
		{"field separate", []string{"api", "user", "-f", "token=short"}, []string{"api", "user", "-f", "token=" + replacement}},
		{"field attached", []string{"api", "user", "-Finput[clientSecret]=short"}, []string{"api", "user", "-Finput[clientSecret]=" + replacement}},
		{"field attached equals", []string{"api", "user", "-f=password=short"}, []string{"api", "user", "-f=password=" + replacement}},
		{"field long equals", []string{"api", "user", "--raw-field=private_key=short"}, []string{"api", "user", "--raw-field=private_key=" + replacement}},
		{"field false positives", []string{"api", "user", "-f", "key=keep", "-F", "keyboard=keep", "--field=tokenizer=keep"}, []string{"api", "user", "-f", "key=keep", "-F", "keyboard=keep", "--field=tokenizer=keep"}},
		{"password flag", []string{"auth", "login", "--password", "short", "--token=another"}, []string{"auth", "login", "--password", replacement, "--token=" + replacement}},
		{"secret body", []string{"secret", "set", "NAME", "--body", "short"}, []string{"secret", "set", "NAME", "--body", replacement}},
		{"secret body attached", []string{"secret", "set", "NAME", "-bshort"}, []string{"secret", "set", "NAME", "-b" + replacement}},
		{"secret body attached equals", []string{"secret", "set", "NAME", "-b=short"}, []string{"secret", "set", "NAME", "-b=" + replacement}},
		{"secret global repo", []string{"-R", "org/repo", "secret", "set", "NAME", "--body=short"}, []string{"-R", "org/repo", "secret", "set", "NAME", "--body=" + replacement}},
		{"variable body", []string{"variable", "set", "NAME", "--body", "keep", "-bkeep"}, []string{"variable", "set", "NAME", "--body", "keep", "-bkeep"}},
		{"token from stdin", []string{"auth", "login", "--with-token"}, []string{"auth", "login", "--with-token"}},
		{"terminator", []string{"api", "user", "--", "--token=keep"}, []string{"api", "user", "--", "--token=keep"}},
		{"missing value", []string{"api", "user", "--token"}, []string{"api", "user", "--token"}},
	}
	r := redact.New(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := append([]string(nil), test.input...)
			got := r.Argv(test.input)
			if test.want == nil {
				header := got[len(got)-1]
				if strings.Contains(header, "short") || !strings.Contains(header, "Authorization:") || !strings.Contains(header, replacement) || got[0] != "api" || got[1] != "user" {
					t.Fatalf("unsafe or damaged header argv: %#v", got)
				}
			} else if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Argv = %#v; want %#v", got, test.want)
			}
			if !reflect.DeepEqual(test.input, before) {
				t.Fatal("Argv mutated input")
			}
		})
	}
	if r.Argv(nil) != nil {
		t.Fatal("nil argv became nonnil")
	}
}

func TestValueSensitiveNamesAndIndependentCopy(t *testing.T) {
	names := []string{"token", "accessToken", "refresh_token", "id-token", "authToken", "GH_TOKEN", "githubToken", "ghEnterpriseToken", "github_enterprise_token", "password", "PASSWD", "secret", "clientSecret", "api_key", "private-key", "credential", "credentials", "Authorization", "proxy_authorization"}
	input := map[string]any{"key": "keep", "keyboard": "keep", "tokenizer": "keep", "publicKey": "keep", "nested": []any{map[string]any{"ordinary": "learned-value"}}}
	for _, name := range names {
		input[name] = map[string]any{"anything": "short"}
	}
	before := mustJSON(t, input)
	out := redact.New(map[string]string{"APP_SECRET": "learned-value"}).Value(input).(map[string]any)
	for _, name := range names {
		if out[name] != replacement {
			t.Errorf("sensitive name %q not redacted", name)
		}
	}
	for _, name := range []string{"key", "keyboard", "tokenizer", "publicKey"} {
		if out[name] != "keep" {
			t.Errorf("ordinary name %q redacted", name)
		}
	}
	if out["nested"].([]any)[0].(map[string]any)["ordinary"] != replacement {
		t.Fatal("nested learned value survived")
	}
	out["nested"].([]any)[0].(map[string]any)["ordinary"] = "changed"
	if !bytes.Equal(before, mustJSON(t, input)) {
		t.Fatal("Value shares mutable input")
	}
	typed := map[string]string{"token": "short", "ordinary": "learned-value"}
	typedOut := redact.New(map[string]string{"APP_SECRET": "learned-value"}).Value(typed).(map[string]string)
	typedOut["ordinary"] = "changed"
	if typed["ordinary"] != "learned-value" || typedOut["token"] != replacement {
		t.Fatal("typed map copied incorrectly")
	}
}

func TestGraphQLInlineDefaultsFragmentsAndVariables(t *testing.T) {
	query := `mutation Test($token:String="defaultsecret", $ordinary:String="keep") @custom(clientSecret:"directivesecret") {
  call(input:{password:"shortsecret", nested:{clientSecret:"nestedsecret", keyboard:"keep"}, token:["listsecret"], private_key:"""blocksecret"""}, ordinary:"keep") { ...F ... on Thing @custom(token:"inlinesecret") { id } }
}
fragment F on Thing @custom(password:"fragmentsecret") { field(secret:"fieldsecret", token:$token) }`
	input := model.Result{
		Source:     model.Source{GraphQL: query, Argv: []string{"api", "graphql", "-f", "query=" + query}},
		Operations: []model.Operation{{Parameters: map[string]any{"query": query, "variables": map[string]any{"token": "variablesecret", "password": "passwordsecret", "clientSecret": "clientsecret", "tokenizer": "keep"}}}},
	}
	before := mustJSON(t, input)
	out := redact.New(nil).Result(input)
	encoded := string(mustJSON(t, out))
	for _, secret := range []string{"defaultsecret", "directivesecret", "shortsecret", "nestedsecret", "listsecret", "blocksecret", "inlinesecret", "fragmentsecret", "fieldsecret", "variablesecret", "passwordsecret", "clientsecret"} {
		if strings.Contains(encoded, secret) {
			t.Errorf("GraphQL secret survived: %s", secret)
		}
	}
	if !strings.Contains(out.Source.GraphQL, "keep") || !strings.Contains(out.Source.GraphQL, "$token") {
		t.Fatal("ordinary literal or variable reference lost")
	}
	if _, err := parser.ParseQuery(&ast.Source{Input: out.Source.GraphQL}); err != nil {
		t.Fatalf("redacted GraphQL is not parseable: %v", err)
	}
	if !bytes.Equal(before, mustJSON(t, input)) {
		t.Fatal("GraphQL result mutated input")
	}
	malformed := redact.New(nil).Result(model.Result{Source: model.Source{GraphQL: `mutation { call(password:"secret")`}})
	if malformed.Source.GraphQL != replacement {
		t.Fatal("malformed GraphQL not withheld")
	}
}

func TestResultAllFieldsAndDeepCopy(t *testing.T) {
	secret := "learned-credential"
	input := model.Result{
		Classification: model.Classification{Level: model.Level(secret), Confidence: model.Confidence(secret), Resolution: model.Resolution(secret)},
		Source:         model.Source{Kind: secret, Argv: []string{secret}, Alias: secret, Method: secret, Path: secret, Host: secret},
		Errors:         []string{secret},
		Operations: []model.Operation{{
			Family: secret, Action: secret, Effect: model.Effect(secret), Execution: model.Execution(secret), Evidence: secret, UnknownReason: model.UnknownReason(secret), Detail: secret,
			Target:     model.Target{Host: secret, Owner: secret, Repository: secret, Number: model.IntPtr(42), NodeID: secret, Branch: secret, BaseBranch: secret, HeadBranch: secret, SHA: secret, Ref: secret, URL: "https://user:short@host"},
			Parameters: map[string]any{secret: []any{secret}},
			Unresolved: []model.Unresolved{{Field: secret, Reason: secret, Source: secret}},
			Origin:     model.Origin{Command: []string{secret}, Method: secret, Route: secret, Field: secret, Alias: secret, Operation: secret, OpType: secret},
		}},
	}
	before := mustJSON(t, input)
	out := redact.New(map[string]string{"GH_TOKEN": secret}).Result(input)
	if strings.Contains(string(mustJSON(t, out)), secret) || out.Operations[0].Target.URL != "https://user:"+replacement+"@host" {
		t.Fatal("Result leaked credential")
	}
	out.Errors[0] = "changed"
	out.Source.Argv[0] = "changed"
	*out.Operations[0].Target.Number = 99
	out.Operations[0].Parameters[replacement].([]any)[0] = "changed"
	out.Operations[0].Unresolved[0].Reason = "changed"
	out.Operations[0].Origin.Command[0] = "changed"
	if !bytes.Equal(before, mustJSON(t, input)) {
		t.Fatal("Result shares mutable input")
	}
}

func TestInputAllFieldsAndEncodedBodies(t *testing.T) {
	secret := "learned-credential"
	body := `{"password":"shortsecret","plain":"` + secret + `","count":9007199254740993}`
	input := protocol.Input{
		Event: secret, EventID: secret, HookID: secret,
		Invocation: protocol.InvocationInfo{Argv: []string{"secret", "set", "NAME", "--body", "argvsecret"}, Expanded: []string{"auth", "login", "--token=expandedsecret"}, Alias: secret, Cwd: secret, RealGh: secret},
		Errors:     []string{secret},
		Operations: []model.Operation{{Parameters: map[string]any{"token": "parametersecret"}, Target: model.Target{Number: model.IntPtr(42)}}},
		Context:    &model.ResolvedContext{Host: secret, Owner: secret, Repository: secret, Branch: secret, HeadSHA: secret, GitDir: secret, Sources: map[string]string{"key": secret}, Unresolved: []model.Unresolved{{Field: secret, Reason: secret, Source: secret}}},
		Policy:     &protocol.PolicyInfo{Mode: secret, Decision: protocol.Decision(secret), Reasons: []string{secret}, Rules: []string{secret}},
		Result:     &protocol.ExecResult{Signal: secret, StartError: secret, Stdout: &protocol.Output{Base64: base64.StdEncoding.EncodeToString([]byte(body)), Size: 123, Truncated: true}, Stderr: &protocol.Output{Base64: base64.StdEncoding.EncodeToString([]byte("\x00" + secret + "\xff")), Size: 321}},
		Previous:   []protocol.HookResult{{HookID: secret, Phase: secret, Outcome: protocol.Outcome(secret), Decision: protocol.Decision(secret), Reason: secret, Message: secret, Effective: protocol.Decision(secret), Error: secret, Stderr: secret}},
	}
	before := mustJSON(t, input)
	out := redact.New(map[string]string{"GH_TOKEN": secret}).Input(input)
	encoded := string(mustJSON(t, out))
	for _, value := range []string{secret, "argvsecret", "expandedsecret", "parametersecret"} {
		if strings.Contains(encoded, value) {
			t.Fatalf("Input leaked %q", value)
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(out.Result.Stdout.Base64)
	if err != nil || strings.Contains(string(decoded), "shortsecret") || strings.Contains(string(decoded), secret) || !strings.Contains(string(decoded), "9007199254740993") {
		t.Fatalf("captured JSON not safely redacted: %q, %v", decoded, err)
	}
	binary, err := base64.StdEncoding.DecodeString(out.Result.Stderr.Base64)
	if err != nil || !bytes.Equal(binary, []byte("\x00"+replacement+"\xff")) {
		t.Fatalf("captured binary not preserved/redacted: %q, %v", binary, err)
	}
	if out.Result.Stdout.Size != 123 || !out.Result.Stdout.Truncated {
		t.Fatal("capture metadata changed")
	}
	out.Invocation.Argv[0] = "changed"
	out.Invocation.Expanded[0] = "changed"
	out.Errors[0] = "changed"
	out.Context.Sources["key"] = "changed"
	out.Context.Unresolved[0].Reason = "changed"
	out.Policy.Reasons[0] = "changed"
	out.Policy.Rules[0] = "changed"
	out.Previous[0].Message = "changed"
	out.Result.Stdout.Base64 = "changed"
	*out.Operations[0].Target.Number = 99
	if !bytes.Equal(before, mustJSON(t, input)) {
		t.Fatal("Input shares mutable input")
	}
}

func TestCapturedMalformedAndGraphQLBodies(t *testing.T) {
	for _, body := range []string{`{"password":"shortsecret"`, `[{"password":"shortsecret"`, `mutation { call(token:"shortsecret") }`} {
		in := protocol.Input{Result: &protocol.ExecResult{Stdout: &protocol.Output{Base64: base64.StdEncoding.EncodeToString([]byte(body))}}}
		out := redact.New(nil).Input(in)
		decoded, err := base64.StdEncoding.DecodeString(out.Result.Stdout.Base64)
		if err != nil || strings.Contains(string(decoded), "shortsecret") {
			t.Fatalf("captured body leaked: %q, %v", decoded, err)
		}
	}
	out := redact.New(nil).Input(protocol.Input{Result: &protocol.ExecResult{Stdout: &protocol.Output{Base64: "not-valid-base64!"}}})
	decoded, err := base64.StdEncoding.DecodeString(out.Result.Stdout.Base64)
	if err != nil || string(decoded) != replacement {
		t.Fatalf("invalid base64 was not withheld: %q, %v", decoded, err)
	}
}

func TestConcurrentRedactor(t *testing.T) {
	r := redact.New(map[string]string{"GH_TOKEN": "learned-credential"})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if got := r.String("prefix learned-credential suffix"); got != "prefix "+replacement+" suffix" {
					t.Errorf("concurrent String = %q", got)
				}
			}
		}()
	}
	wg.Wait()
}

func TestValueByteBodiesAndNilContainers(t *testing.T) {
	r := redact.New(nil)
	input := json.RawMessage(`{"token":"short","key":"keep"}`)
	before := append(json.RawMessage(nil), input...)
	out := r.Value(input).(json.RawMessage)
	if strings.Contains(string(out), "short") || !strings.Contains(string(out), "keep") {
		t.Fatalf("raw JSON body: %q", out)
	}
	out[0] = 'x'
	if !bytes.Equal(input, before) {
		t.Fatal("RawMessage shares input")
	}
	for _, value := range []any{map[string]any(nil), map[string]string(nil), []any(nil), []string(nil), []byte(nil), json.RawMessage(nil), nil, true, 42, json.Number("123")} {
		if !reflect.DeepEqual(value, r.Value(value)) {
			t.Fatalf("nil/scalar changed: %#v", value)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestValueTypedJSONContainers(t *testing.T) {
	type text string
	input := map[text][]map[string]any{
		"nested": {{"token": "short", "ordinary": "learned-credential", "numbers": []int{1, 2}}},
	}
	before := mustJSON(t, input)
	out := redact.New(map[string]string{"GH_TOKEN": "learned-credential"}).Value(input).(map[string]any)
	nested := out["nested"].([]any)[0].(map[string]any)
	if nested["token"] != replacement || nested["ordinary"] != replacement {
		t.Fatal("typed nested JSON credentials survived")
	}
	nested["numbers"].([]any)[0] = 99
	nested["token"] = "changed"
	if !bytes.Equal(before, mustJSON(t, input)) {
		t.Fatal("typed JSON container shares input")
	}
	words := []string{"learned-credential", "keep"}
	copied := redact.New(map[string]string{"GH_TOKEN": "learned-credential"}).Value(words).([]string)
	copied[1] = "changed"
	if words[1] != "keep" || copied[0] != replacement {
		t.Fatal("typed string slice shares input or leaks")
	}
}

func FuzzRedactString(f *testing.F) {
	for _, seed := range []struct{ input, secret string }{
		{"before learned-credential after", "learned-credential"},
		{"[REDACTED]", "[REDACTED]"},
		{"Bearer short", "REDACTED"},
		{"xabcdefghy", "abcdefgh"},
		{"aaaaa", "a"},
		{"\x00\xffsecret\x00", "\xffsecret"},
		{"plain text", ""},
	} {
		f.Add(seed.input, seed.secret)
	}
	f.Fuzz(func(t *testing.T, input, secret string) {
		r := redact.New(map[string]string{"GH_TOKEN": secret, "SECOND_SECRET": "fuzz-fixed-credential", "THIRD_SECRET": "x[REDACTED]y"})
		out := r.String(input + secret + "fuzz-fixed-credential")
		for _, value := range []string{secret, "fuzz-fixed-credential", "x[REDACTED]y"} {
			if value != "" && strings.Contains(out, value) {
				t.Fatalf("output %q contains learned value %q", out, value)
			}
		}
	})
}

func BenchmarkRedactorResult(b *testing.B) {
	r := redact.New(map[string]string{"GH_TOKEN": "fake-learned-github-credential"})
	input := model.Result{
		SchemaVersion:  model.SchemaVersion,
		Classification: model.Classification{Level: model.LevelL3, Confidence: model.ConfidenceExact, Resolution: model.ResolutionComplete},
		Source:         model.Source{Kind: "graphql", Argv: []string{"api", "graphql", "-f", "token=fake-learned-github-credential"}, GraphQL: `mutation { call(input:{token:"short", ordinary:"keep"}) { id } }`},
		Operations:     []model.Operation{{Family: "pr.merge", Action: "pr.merge.direct", Effect: model.EffectWrite, Execution: model.ExecutionWillRun, Target: model.Target{Repository: "org/repo", Number: model.IntPtr(42)}, Parameters: map[string]any{"variables": map[string]any{"token": "fake-learned-github-credential", "pullRequestId": "PR_fake"}, "nested": []any{map[string]any{"ordinary": "keep"}}}}},
	}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkResult = r.Result(input)
	}
}

var benchmarkResult model.Result

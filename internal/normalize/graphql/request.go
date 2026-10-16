package graphql

import (
	"bytes"
	"net"
	"net/url"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize/apiargs"
)

const maxInputBytes = 1 << 20
const maxDepth = 256
const maxSelections = 10000
const maxSourceBytes = 64 << 10

type requestData struct {
	query         string
	operationName string
	variables     map[string]any
	unavailable   map[string]model.Unresolved
	note          string
}

type inputError struct {
	reason model.UnknownReason
	detail string
}

func (e *inputError) Error() string                        { return e.detail }
func fail(reason model.UnknownReason, detail string) error { return &inputError{reason, detail} }

func unavailableReason(detail string) model.UnknownReason {
	s := strings.ToLower(detail)
	if strings.Contains(s, "too large") || strings.Contains(s, "too-large") || strings.Contains(s, "size limit") || strings.Contains(s, "exceeds") {
		return model.UnknownInputTooLarge
	}
	return model.UnknownInputUnavailable
}

func readInput(inv model.Invocation, path string) ([]byte, error) {
	var b []byte
	if path == "-" {
		if inv.Stdin.Status != model.StdinCaptured {
			return nil, fail(unavailableReason(string(inv.Stdin.Status)), "stdin not captured ("+string(inv.Stdin.Status)+")")
		}
		b = inv.Stdin.Data
	} else {
		if inv.ReadFile == nil {
			return nil, fail(model.UnknownInputUnavailable, "input file unavailable")
		}
		var err error
		b, err = inv.ReadFile(path)
		if err != nil {
			return nil, fail(unavailableReason(err.Error()), "input file unavailable: "+err.Error())
		}
	}
	if len(b) > maxInputBytes {
		return nil, fail(model.UnknownInputTooLarge, "input exceeds 1 MiB")
	}
	return b, nil
}

func reconstruct(inv model.Invocation, req model.APIRequest) (requestData, error) {
	d := requestData{variables: map[string]any{}, unavailable: map[string]model.Unresolved{}}
	for _, f := range req.Fields {
		if len(f.Value) > maxInputBytes || len(f.Key) > maxInputBytes {
			return d, fail(model.UnknownInputTooLarge, "field exceeds 1 MiB")
		}
	}
	if apiargs.NeedsStdin(req) && len(inv.Stdin.Data) > maxInputBytes {
		return d, fail(model.UnknownInputTooLarge, "stdin exceeds 1 MiB")
	}
	bounded := inv
	oversized := false
	if inv.ReadFile != nil {
		bounded.ReadFile = func(path string) ([]byte, error) {
			b, err := inv.ReadFile(path)
			if len(b) > maxInputBytes {
				oversized = true
				return nil, fail(model.UnknownInputTooLarge, "field file exceeds 1 MiB")
			}
			return b, err
		}
	}
	params, unresolved, err := apiargs.BuildParams(req, bounded)
	if oversized {
		return d, fail(model.UnknownInputTooLarge, "field file exceeds 1 MiB")
	}
	if err != nil {
		return d, fail(model.UnknownMalformed, err.Error())
	}
	var payload map[string]any
	if req.Input != "" {
		b, err := readInput(inv, req.Input)
		if err != nil {
			return d, err
		}
		// Decode only an object: GitHub does not support batched GraphQL bodies.
		if bytes.HasPrefix(bytes.TrimSpace(b), []byte("[")) {
			return d, fail(model.UnknownMalformed, "GraphQL batching is unsupported")
		}
		if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
			return d, fail(model.UnknownMalformed, "GraphQL body must be an object")
		}
		payload, err = apiargs.DecodeObject(b)
		if err != nil {
			return d, fail(model.UnknownParseError, "invalid GraphQL JSON body: "+err.Error())
		}
		if v := payload["variables"]; v != nil {
			var ok bool
			d.variables, ok = v.(map[string]any)
			if !ok {
				return d, fail(model.UnknownMalformed, "variables must be an object")
			}
		}
		d.note = "--input fields are URL query parameters, not GraphQL variables"
	} else {
		payload = params
		for _, u := range unresolved {
			key := strings.TrimPrefix(u.Field, "fields.")
			top, _, _ := strings.Cut(key, "[")
			if top == "query" || top == "operationName" {
				return d, fail(unavailableReason(u.Reason), u.Reason)
			}
			u.Field = "variables." + top
			d.unavailable[top] = u
		}
		// gh only calls groupGraphQLVariables for the literal endpoint and non-GET.
		if req.Endpoint == "graphql" && !strings.EqualFold(req.Method, "GET") {
			for k, v := range params {
				if k != "query" && k != "operationName" {
					d.variables[k] = v
				}
			}
		} else if strings.EqualFold(req.Method, "GET") {
			d.note = "GET sends fields as URL query parameters; classification describes the document, not server acceptance"
		} else {
			d.note = "non-literal endpoint: gh does not group fields into GraphQL variables"
			if v, exists := params["variables"]; exists {
				var ok bool
				d.variables, ok = v.(map[string]any)
				if !ok {
					return d, fail(model.UnknownMalformed, "variables must be an object")
				}
			}
		}
	}
	q, ok := payload["query"].(string)
	if !ok || strings.TrimSpace(q) == "" {
		return d, fail(model.UnknownMalformed, "query must be a non-empty string")
	}
	d.query = q
	if len(q) > maxInputBytes {
		return d, fail(model.UnknownInputTooLarge, "document exceeds 1 MiB")
	}
	if v := payload["operationName"]; v != nil {
		d.operationName, ok = v.(string)
		if !ok {
			return d, fail(model.UnknownMalformed, "operationName must be a string")
		}
	}
	return d, nil
}

func sourceFor(inv model.Invocation, req model.APIRequest, query string) model.Source {
	host := req.Hostname
	if host == "" {
		host = inv.Env["GH_HOST"]
	}
	path := req.Endpoint
	if u, err := url.Parse(req.Endpoint); err == nil && u.IsAbs() {
		host, path = u.Host, u.Path
	}
	host = instanceHost(host)
	if len(query) > maxSourceBytes {
		query = query[:maxSourceBytes]
	}
	return model.Source{Kind: "graphql", Method: req.Method, Path: path, Host: host, GraphQL: query}
}

// API hosts identify the same instance used by gh configuration and policies.
func instanceHost(host string) string {
	u := url.URL{Host: host}
	name := strings.ToLower(u.Hostname())
	if name == "api.github.com" || (strings.HasPrefix(name, "api.") && strings.HasSuffix(name, ".ghe.com")) {
		name = strings.TrimPrefix(name, "api.")
	}
	if port := u.Port(); port != "" && port != "80" && port != "443" {
		return net.JoinHostPort(name, port)
	}
	if strings.Contains(name, ":") {
		return "[" + name + "]"
	}
	return name
}

func unknownResult(source model.Source, err error) model.Result {
	reason := model.UnknownParseError
	if e, ok := err.(*inputError); ok {
		reason = e.reason
	}
	r := model.NewUnknown(model.LevelL3, reason, err.Error())
	r.Source = source
	r.Errors = []string{err.Error()}
	return r
}

package native

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

func parseRepository(value, host string) (model.Target, bool) {
	t := model.Target{}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh") {
			return t, false
		}
		host = u.Hostname()
		value = strings.Trim(u.Path, "/")
		t.URL = u.String()
	}
	parts := strings.Split(value, "/")
	if len(parts) == 3 {
		host = parts[0]
		parts = parts[1:]
	}
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(value, " \t\n?#") {
		return model.Target{}, false
	}
	if host == "" {
		host = "github.com"
	}
	t.Host = strings.ToLower(strings.TrimPrefix(host, "www."))
	t.Owner = parts[0]
	t.Repository = parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
	return t, true
}

func parseNumberURL(value string) (model.Target, bool) {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return model.Target{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || (parts[2] != "pull" && parts[2] != "issues") {
		return model.Target{}, false
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 {
		return model.Target{}, false
	}
	t, ok := parseRepository(parts[0]+"/"+parts[1], u.Hostname())
	if !ok {
		return model.Target{}, false
	}
	t.Number = model.IntPtr(n)
	t.URL = value
	return t, true
}

func extractTarget(op *model.Operation, c commandSpec, p parsedArgs, inv model.Invocation) {
	pos := p.positionals
	if p.dashIndex >= 0 && !p.ambiguous && (c.path == "repo clone" || c.path == "repo fork") {
		op.Parameters["gitArguments"] = pos[p.dashIndex:]
		pos = pos[:p.dashIndex]
	}
	if p.ambiguous {
		pos = nil
	}
	arg := ""
	if len(pos) > 0 {
		arg = pos[0]
	}
	host := inv.Env["GH_HOST"]
	repo := p.value("repo")
	if repo == "" {
		repo = inv.Env["GH_REPO"]
	}
	scoped := c.repo || c.positional == "repo"
	// Organization/user secrets, variables and rulesets do not act on cwd.
	if (strings.HasPrefix(c.path, "secret ") || strings.HasPrefix(c.path, "variable ") || strings.HasPrefix(c.path, "ruleset ")) && (p.value("org") != "" || p.enabled("user")) {
		scoped = false
		op.Target.Owner = p.value("org")
	}
	if scoped && repo != "" && (c.positional != "repo" || arg == "") && ((c.positional != "pr" && c.positional != "issue") || !strings.Contains(arg, "://")) {
		if t, ok := parseRepository(repo, host); ok {
			op.Target = t
		} else {
			unresolved(op, "target.repository", "invalid repository selector", "argv")
		}
	}
	if host != "" && op.Target.Host == "" {
		op.Target.Host = host
	}
	if p.value("hostname") != "" {
		op.Target.Host = p.value("hostname")
	}
	if p.value("host") != "" {
		op.Target.Host = p.value("host")
	}
	switch c.positional {
	case "pr", "issue":
		if arg != "" {
			if t, ok := parseNumberURL(arg); ok {
				op.Target = t
			} else if n, err := strconv.Atoi(strings.TrimPrefix(arg, "#")); err == nil && n > 0 {
				op.Target.Number = model.IntPtr(n)
			} else if c.positional == "pr" && !strings.Contains(arg, "://") {
				op.Target.HeadBranch = arg
				unresolved(op, "target.number", "PR number for head branch", "api")
			} else {
				unresolved(op, "target.number", "invalid issue/PR selector", "argv")
			}
		} else if c.positional == "pr" {
			unresolved(op, "target.headBranch", "current branch", "git")
			unresolved(op, "target.number", "PR for current branch", "api")
		} else {
			unresolved(op, "target.number", "issue selector required", "argv")
		}
	case "repo":
		if arg != "" {
			if t, ok := parseRepository(arg, host); ok {
				op.Target = t
			} else {
				unresolved(op, "target.repository", "repository selector requires owner/name or URL", "argv")
			}
		}
		if len(pos) > 1 && c.path == "repo clone" {
			op.Parameters["directory"] = pos[1]
		}
	case "repo-create":
		if arg != "" {
			if t, ok := parseRepository(arg, host); ok {
				op.Target = t
			} else if !strings.ContainsAny(arg, "/: \t") {
				op.Parameters["name"] = arg
				unresolved(op, "target.owner", "authenticated owner for new repository", "api")
				unresolved(op, "target.repository", "owner of new repository", "api")
			} else {
				unresolved(op, "target.repository", "invalid new repository name", "argv")
			}
		} else {
			unresolved(op, "target.repository", "repository name selected interactively", "interactive")
		}
		scoped = false
	case "repo-list":
		if arg != "" {
			op.Target.Owner = arg
		} else {
			unresolved(op, "target.owner", "authenticated owner", "api")
		}
		scoped = false
	case "rename":
		if arg != "" {
			op.Parameters["newName"] = arg
		} else {
			unresolved(op, "parameters.newName", "new name selected interactively", "interactive")
		}
	case "tag":
		if arg != "" {
			op.Target.Ref = arg
			op.Parameters["tag"] = arg
		} else {
			unresolved(op, "target.ref", "release selected interactively or latest release", "api")
		}
		if len(pos) > 1 {
			if c.path == "release delete-asset" || c.path == "release verify-asset" {
				op.Parameters["asset"] = pos[1]
			} else {
				op.Parameters["assets"] = pos[1:]
			}
		}
	case "name":
		if arg != "" {
			op.Parameters["name"] = arg
		} else if strings.HasSuffix(c.path, " set") && p.present("env-file") {
			unresolved(op, "parameters.names", "names loaded from dotenv file", "file")
		} else if !strings.HasSuffix(c.path, " list") {
			unresolved(op, "parameters.name", "name required or selected interactively", "argv")
		}
	case "workflow", "run", "ruleset":
		if arg != "" {
			op.Parameters[c.positional] = arg
			if c.positional == "ruleset" && c.path == "ruleset check" {
				op.Target.Branch = arg
			}
		} else if !strings.HasSuffix(c.path, " list") {
			unresolved(op, "parameters."+c.positional, "target selected interactively or via API", "api")
		}
	default:
		if len(pos) > 0 {
			op.Parameters["arguments"] = pos
		}
	}
	if c.path == "issue transfer" {
		if len(pos) > 1 {
			op.Parameters["destinationRepository"] = pos[1]
		} else {
			unresolved(op, "parameters.destinationRepository", "destination required", "argv")
		}
	}
	if p.value("name") != "" && c.path == "label edit" {
		op.Parameters["newName"] = p.value("name")
	}
	if c.path == "label clone" && arg != "" {
		op.Parameters["sourceRepository"] = arg
	}
	if c.path == "pr create" {
		if p.value("head") == "" {
			unresolved(op, "target.headBranch", "current branch", "git")
		}
		if p.value("base") == "" {
			unresolved(op, "target.baseBranch", "default base branch", "api")
		}
	}
	op.Target.BaseBranch = p.value("base")
	if v := p.value("head"); v != "" {
		op.Target.HeadBranch = v
	}
	if v := p.value("branch"); v != "" {
		op.Target.Branch = v
	}
	if v := p.value("ref"); v != "" {
		op.Target.Ref = v
	}
	if v := p.value("commit"); v != "" {
		op.Target.SHA = v
	}
	if c.path == "workflow run" {
		extractWorkflowFields(op, p)
	}
	if scoped && op.Target.Repository == "" {
		unresolved(op, "target.repository", "repository from local git context", "git")
		if op.Target.Owner == "" {
			unresolved(op, "target.owner", "repository owner from local git context", "git")
		}
	}
	if op.Target.Host == "" {
		if scoped && op.Target.Repository == "" {
			unresolved(op, "target.host", "host from local git remotes", "git")
		} else {
			op.Target.Host = "github.com"
		}
	}
}

func extractWorkflowFields(op *model.Operation, p parsedArgs) {
	inputs := make(map[string]any)
	for _, name := range []string{"raw-field", "field"} {
		for _, v := range p.flags[name] {
			k, val, ok := strings.Cut(v, "=")
			if !ok {
				unresolved(op, "parameters.inputs", "workflow input requires key=value", "argv")
				continue
			}
			if name == "field" && strings.HasPrefix(val, "@") {
				unresolved(op, "parameters.inputs."+k, "workflow input read from file/stdin", "file")
			} else {
				inputs[k] = val
			}
		}
	}
	if len(inputs) > 0 {
		op.Parameters["inputs"] = inputs
	}
	if p.enabled("json") {
		unresolved(op, "parameters.inputs", "JSON workflow inputs from stdin", "stdin")
	}
}

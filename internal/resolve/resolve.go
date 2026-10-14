// Package resolve fills deferred normalization targets without invoking git.
package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mkusaka/ghok/internal/model"
)

// Resolver belongs to one invocation. Configure its public fields before use
// and do not mutate them thereafter. Local and Fill may run concurrently on
// distinct Results. A nil Env snapshots the process environment on first use;
// a non-nil Env is the complete environment, not an overlay.
// The caller must supply a discovered real gh executable, never the shim.
type Resolver struct {
	Env     map[string]string
	Cwd     string
	RealGh  string
	Timeout time.Duration

	mu    sync.Mutex
	env   map[string]string
	local *localInfo
	cache map[string]apiResult
}

type apiResult struct {
	data []byte
	err  error
}

func (r *Resolver) localLocked() *localInfo {
	if r.local == nil {
		r.env = make(map[string]string)
		if r.Env == nil {
			for _, entry := range os.Environ() {
				k, v, _ := strings.Cut(entry, "=")
				r.env[k] = v
			}
		} else {
			for k, v := range r.Env {
				r.env[k] = v
			}
		}
		info := resolveLocal(r.Cwd, r.env)
		r.local = &info
	}
	return r.local
}

// Local resolves environment and local git metadata only, with no subprocess
// or network. Results are cached for the lifetime of this Resolver. A returned
// candidate repository can still have an ambiguity entry in Unresolved.
func (r *Resolver) Local() model.ResolvedContext {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.localLocked().context
	sources := make(map[string]string, len(c.Sources))
	for k, v := range c.Sources {
		sources[k] = v
	}
	c.Sources = sources
	c.Unresolved = append([]model.Unresolved(nil), c.Unresolved...)
	return c
}

func targetValue(t model.Target, field string) bool {
	switch field {
	case "target.repository":
		return t.Repository != ""
	case "target.owner":
		return t.Owner != ""
	case "target.host":
		return t.Host != ""
	case "target.number":
		return t.Number != nil
	case "target.branch":
		return t.Branch != ""
	case "target.headBranch":
		return t.HeadBranch != ""
	case "target.baseBranch":
		return t.BaseBranch != ""
	case "target.sha":
		return t.SHA != ""
	case "target.ref":
		return t.Ref != ""
	case "target.nodeId":
		return t.NodeID != ""
	case "target.url":
		return t.URL != ""
	}
	return false
}

func setUnresolved(op *model.Operation, field, reason, source string) {
	for i := range op.Unresolved {
		if op.Unresolved[i].Field == field {
			op.Unresolved[i].Reason = reason
			op.Unresolved[i].Source = source
			return
		}
	}
	op.Unresolved = append(op.Unresolved, model.Unresolved{Field: field, Reason: reason, Source: source})
}

// Fill fills local deferred targets first. API calls require both allowAPI and
// a missing target field explicitly named in need (target.repository, owner,
// or target.number). It never executes the user's original command.
// Diagnostics are returned, not appended to Result.Errors. Unknown operation
// identity/effect and unrelated unresolved parameters are never cleared.
func (r *Resolver) Fill(ctx context.Context, res *model.Result, need []string, allowAPI bool) []string {
	if res == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	local := r.localLocked()
	requested := make(map[string]bool, len(need))
	for _, field := range need {
		requested[field] = true
	}
	var diagnostics []string
	for i := range res.Operations {
		op := &res.Operations[i]
		if op.Execution == model.ExecutionSkipped || op.Execution == model.ExecutionNotSelected {
			continue
		}
		t := &op.Target
		inherited := make(map[string]bool)
		// Node IDs identify their own repositories. The cwd is not evidence for
		// the repository of an arbitrary node, even when it supplies the API host.
		if t.NodeID == "" && res.Classification.Level != model.LevelL3 {
			if t.Repository == "" && (hasUnresolved(op.Unresolved, "target.repository") || requested["target.repository"] || hasUnresolved(op.Unresolved, "target.owner")) {
				t.Repository = local.context.Repository
				inherited["target.repository"] = true
				if t.Owner == "" {
					t.Owner = local.context.Owner
					inherited["target.owner"] = true
				}
			}
			if t.Repository != "" && t.Owner == "" {
				t.Owner, _, _ = strings.Cut(t.Repository, "/")
			}
		}
		if t.Host == "" && (t.NodeID != "" || t.Repository != "" || hasUnresolved(op.Unresolved, "target.host") || requested["target.host"]) {
			t.Host = local.context.Host
			inherited["target.host"] = true
		}
		for _, field := range []string{"target.branch", "target.headBranch", "target.baseBranch", "target.sha"} {
			if targetValue(*t, field) || (!hasUnresolved(op.Unresolved, field) && !requested[field]) || t.NodeID != "" || res.Classification.Level == model.LevelL3 {
				continue
			}
			switch field {
			case "target.branch":
				t.Branch = local.context.Branch
			case "target.headBranch":
				t.HeadBranch = local.context.Branch
			case "target.baseBranch":
				t.BaseBranch = local.baseBranch
			case "target.sha":
				t.SHA = local.context.HeadSHA
			}
			inherited[field] = true
		}
		currentPR := res.Classification.Level == model.LevelL1 && (strings.HasPrefix(op.Family, "pr.") || len(op.Origin.Command) > 0 && op.Origin.Command[0] == "pr") && op.Family != "pr.create" && op.Action != "pr.create" && (hasUnresolved(op.Unresolved, "target.number") || requested["target.number"])
		if currentPR && t.Number == nil && t.NodeID == "" && t.HeadBranch == "" {
			if t.Branch != "" {
				t.HeadBranch = t.Branch
			} else {
				t.HeadBranch = local.context.Branch
			}
		}
		for _, u := range local.context.Unresolved {
			if inherited[u.Field] {
				setUnresolved(op, u.Field, u.Reason, u.Source)
			}
		}
		// Track locally ambiguous fields so a candidate never becomes a confirmed
		// target merely because its string is non-empty.
		blocked := make(map[string]bool)
		for _, u := range local.context.Unresolved {
			if inherited[u.Field] {
				blocked[u.Field] = true
			}
			for _, existing := range op.Unresolved {
				if existing.Field == u.Field && existing.Source == u.Source && existing.Reason == u.Reason {
					blocked[u.Field] = true
				}
			}
		}
		if allowAPI {
			nodeNeeded := false
			for _, field := range []string{"target.repository", "target.owner", "target.number"} {
				if requested[field] && !targetValue(*t, field) {
					nodeNeeded = true
				}
			}
			if t.NodeID != "" && nodeNeeded {
				if t.Host == "" || blocked["target.host"] {
					for _, field := range need {
						if !targetValue(*t, field) {
							setUnresolved(op, field, "API hostname is unresolved", "api")
						}
					}
				} else {
					node, err := r.node(ctx, t.Host, t.NodeID)
					if err == nil && strings.HasPrefix(op.Family, "pr.") && node.Type != "PullRequest" {
						err = fmt.Errorf("node is not a PullRequest")
					}
					if err == nil && strings.HasPrefix(op.Family, "issue.") && node.Type != "Issue" {
						err = fmt.Errorf("node is not an Issue")
					}
					if err == nil && t.Repository != "" && !strings.EqualFold(t.Repository, node.Repository) {
						err = fmt.Errorf("node repository differs from explicit target")
					}
					if err != nil {
						diagnostics = append(diagnostics, err.Error())
						for _, field := range need {
							if !targetValue(*t, field) {
								setUnresolved(op, field, err.Error(), "api")
							}
						}
					} else {
						if requested["target.repository"] && t.Repository == "" {
							t.Repository = node.Repository
						}
						if (requested["target.owner"] || t.Repository != "") && t.Owner == "" {
							t.Owner, _, _ = strings.Cut(node.Repository, "/")
						}
						if requested["target.number"] && t.Number == nil && node.Number > 0 {
							t.Number = model.IntPtr(node.Number)
						}
						for _, field := range need {
							if !targetValue(*t, field) {
								setUnresolved(op, field, "node does not supply requested field", "api")
							}
						}
					}
				}
			} else if requested["target.number"] && t.Number == nil && currentPR && t.NodeID == "" {
				reason := ""
				switch {
				case t.Repository == "" || t.Owner == "" || t.Host == "":
					reason = "repository or API hostname is unresolved"
				case blocked["target.repository"] || blocked["target.host"]:
					reason = "local repository or host is ambiguous"
				case t.HeadBranch == "":
					reason = "current branch is unresolved"
				}
				if reason != "" {
					setUnresolved(op, "target.number", reason, "api")
				} else {
					number, err := r.prNumber(ctx, *t)
					if err != nil {
						diagnostics = append(diagnostics, err.Error())
						setUnresolved(op, "target.number", err.Error(), "api")
					} else {
						t.Number = model.IntPtr(number)
					}
				}
			}
		}
		for _, field := range need {
			if strings.HasPrefix(field, "target.") && !targetValue(*t, field) && !hasUnresolved(op.Unresolved, field) {
				setUnresolved(op, field, "requested target field could not be resolved", "resolver")
			}
		}
		remaining := op.Unresolved[:0]
		for _, u := range op.Unresolved {
			if !targetValue(*t, u.Field) || blocked[u.Field] {
				remaining = append(remaining, u)
			}
		}
		op.Unresolved = remaining
	}
	updateResolution(res)
	return diagnostics
}

func updateResolution(res *model.Result) {
	unresolved, known, unknown := false, false, false
	for _, op := range res.Operations {
		if op.Execution == model.ExecutionSkipped || op.Execution == model.ExecutionNotSelected {
			continue
		}
		unresolved = unresolved || len(op.Unresolved) > 0
		unknown = unknown || op.Family == model.FamilyUnknown || op.Effect == model.EffectUnknown || op.UnknownReason != model.UnknownNone
		t := op.Target
		known = known || t.Repository != "" || t.Host != "" || t.Number != nil || t.NodeID != "" || t.Branch != "" || t.HeadBranch != "" || t.SHA != "" || t.Ref != ""
	}
	switch {
	case unknown:
		res.Classification.Resolution = model.ResolutionUnresolved
	case !unresolved:
		res.Classification.Resolution = model.ResolutionComplete
	case known:
		res.Classification.Resolution = model.ResolutionPartial
	default:
		res.Classification.Resolution = model.ResolutionUnresolved
	}
}

const nodeQuery = `query($id:ID!){node(id:$id){__typename ... on PullRequest{number repository{nameWithOwner}} ... on Issue{number repository{nameWithOwner}} ... on Ref{name repository{nameWithOwner}} ... on Repository{nameWithOwner}}}`

type nodeResult struct {
	Type, Repository string
	Number           int
}

func (r *Resolver) node(ctx context.Context, host, id string) (nodeResult, error) {
	data, err := r.call(ctx, []string{"api", "graphql", "--hostname", host, "-f", "query=" + nodeQuery, "-f", "id=" + id})
	if err != nil {
		return nodeResult{}, err
	}
	var response struct {
		Data struct {
			Node *struct {
				Type          string `json:"__typename"`
				Number        int    `json:"number"`
				NameWithOwner string `json:"nameWithOwner"`
				Repository    struct {
					NameWithOwner string `json:"nameWithOwner"`
				} `json:"repository"`
			} `json:"node"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nodeResult{}, fmt.Errorf("node lookup: invalid JSON")
	}
	if len(response.Errors) > 0 {
		return nodeResult{}, fmt.Errorf("node lookup: GraphQL errors")
	}
	n := response.Data.Node
	if n == nil {
		return nodeResult{}, fmt.Errorf("node lookup: node not found")
	}
	repo := n.Repository.NameWithOwner
	switch n.Type {
	case "Repository":
		repo = n.NameWithOwner
	case "PullRequest", "Issue":
		if n.Number <= 0 {
			return nodeResult{}, fmt.Errorf("node lookup: invalid number")
		}
	case "Ref":
		// A Ref's name alone does not distinguish a branch from a tag.
	default:
		return nodeResult{}, fmt.Errorf("node lookup: unsupported node type")
	}
	_, repo, err = fullName(repo, host)
	if err != nil {
		return nodeResult{}, fmt.Errorf("node lookup: invalid repository")
	}
	return nodeResult{Type: n.Type, Repository: repo, Number: n.Number}, nil
}

func (r *Resolver) prNumber(ctx context.Context, t model.Target) (int, error) {
	parts := strings.Split(t.Repository, "/")
	if len(parts) != 2 || !validRepoPart(parts[0]) || !validRepoPart(parts[1]) || !validRepoPart(t.Owner) {
		return 0, fmt.Errorf("PR lookup: invalid repository")
	}
	endpoint := "repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/pulls?head=" + url.QueryEscape(t.Owner+":"+t.HeadBranch) + "&state=open&per_page=2"
	data, err := r.call(ctx, []string{"api", endpoint, "--hostname", t.Host, "--method", "GET"})
	if err != nil {
		return 0, err
	}
	var pulls []struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(data, &pulls); err != nil {
		return 0, fmt.Errorf("PR lookup: invalid JSON")
	}
	if len(pulls) != 1 {
		return 0, fmt.Errorf("PR lookup: expected exactly one open pull request, found %d", len(pulls))
	}
	if pulls[0].Number <= 0 {
		return 0, fmt.Errorf("PR lookup: invalid number")
	}
	return pulls[0].Number, nil
}

func (r *Resolver) call(ctx context.Context, args []string) ([]byte, error) {
	if r.cache == nil {
		r.cache = make(map[string]apiResult)
	}
	key := strings.Join(args, "\x00")
	if result, ok := r.cache[key]; ok {
		return result.data, result.err
	}
	var result apiResult
	if !filepath.IsAbs(r.RealGh) {
		result.err = fmt.Errorf("resolver requires an absolute real gh path")
	} else {
		timeout := r.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		result.data, result.err = runAPI(callCtx, r.RealGh, r.Cwd, r.env, args)
		cancel()
	}
	r.cache[key] = result
	return result.data, result.err
}

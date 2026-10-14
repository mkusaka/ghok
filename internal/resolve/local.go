package resolve

import (
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

const maxBytes = 1 << 20

type localInfo struct {
	context    model.ResolvedContext
	baseBranch string
}

type remote struct{ name, fetch, push, resolved string }

func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err == nil && len(data) > maxBytes {
		return nil, fmt.Errorf("git metadata exceeds 1 MiB")
	}
	return data, err
}

func absolute(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

func gitDirectory(cwd string, env map[string]string) (string, error) {
	if dir := env["GIT_DIR"]; dir != "" {
		return absolute(cwd, dir), nil
	}
	if work := env["GIT_WORK_TREE"]; work != "" {
		cwd = absolute(cwd, work)
	}
	ceilings := make(map[string]bool)
	for _, dir := range filepath.SplitList(env["GIT_CEILING_DIRECTORIES"]) {
		if filepath.IsAbs(dir) {
			if real, err := filepath.EvalSymlinks(dir); err == nil {
				dir = real
			}
			ceilings[filepath.Clean(dir)] = true
		}
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		// Git does not search the ceiling itself; starting inside it is allowed.
		if dir != cwd && ceilings[dir] {
			break
		}
		candidate := filepath.Join(dir, ".git")
		info, err := os.Stat(candidate)
		if err == nil {
			if info.IsDir() {
				return candidate, nil
			}
			data, err := readBounded(candidate)
			if err != nil {
				return "", err
			}
			text := strings.TrimSpace(string(data))
			if !strings.HasPrefix(text, "gitdir: ") || strings.TrimSpace(text[8:]) == "" {
				return "", fmt.Errorf("malformed .git file")
			}
			return absolute(dir, strings.TrimSpace(text[8:])), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return "", fmt.Errorf("no git repository found")
}

func validSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func readRef(gitdir, common, ref string, depth int) (string, error) {
	if depth > 10 {
		return "", fmt.Errorf("git symbolic ref depth exceeded")
	}
	if !strings.HasPrefix(ref, "refs/") || strings.Contains(ref, "\\") || path.Clean(ref) != ref || strings.Contains(ref, "..") {
		return "", fmt.Errorf("invalid git ref")
	}
	for _, dir := range []string{gitdir, common} {
		data, err := readBounded(filepath.Join(dir, filepath.FromSlash(ref)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		value := strings.TrimSpace(string(data))
		if strings.HasPrefix(value, "ref: ") {
			return readRef(gitdir, common, strings.TrimSpace(value[5:]), depth+1)
		}
		if !validSHA(value) {
			return "", fmt.Errorf("invalid git ref SHA")
		}
		return value, nil
	}
	data, err := readBounded(filepath.Join(common, "packed-refs"))
	if os.IsNotExist(err) {
		return "", fmt.Errorf("branch has no commit (unborn or missing ref)")
	}
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == ref {
			if !validSHA(fields[0]) {
				return "", fmt.Errorf("invalid packed ref SHA")
			}
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("branch has no commit (unborn or missing ref)")
}

func normalizeHost(host string) string {
	host = strings.ToLower(host)
	host = strings.TrimPrefix(host, "www.")
	if host == "ssh.github.com" {
		return "github.com"
	}
	return host
}

func parseRemote(raw string) (host, repository string, ssh bool, err error) {
	if !strings.Contains(raw, "://") && strings.Contains(raw, ":") && !strings.Contains(raw, "\\") {
		raw = "ssh://" + strings.Replace(raw, ":", "/", 1)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", false, fmt.Errorf("invalid remote URL")
	}
	switch u.Scheme {
	case "https", "http", "git", "git+https":
	case "ssh", "git+ssh":
		ssh = true
	default:
		return "", "", false, fmt.Errorf("unsupported remote URL")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Hostname() == "" || len(parts) != 2 {
		return "", "", ssh, fmt.Errorf("invalid remote repository path")
	}
	parts[1] = strings.TrimSuffix(parts[1], ".git")
	if !validRepoPart(parts[0]) || !validRepoPart(parts[1]) {
		return "", "", ssh, fmt.Errorf("invalid remote repository name")
	}
	return normalizeHost(u.Hostname()), strings.Join(parts, "/"), ssh, nil
}

func validRepoPart(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\ \t\n\r?#:{}")
}

func fullName(raw, fallback string) (host, repository string, err error) {
	if strings.Contains(raw, "://") {
		host, repository, _, err = parseRemote(raw)
		return
	}
	parts := strings.Split(raw, "/")
	switch len(parts) {
	case 2:
		host = fallback
	case 3:
		host = normalizeHost(parts[0])
		parts = parts[1:]
	default:
		return "", "", fmt.Errorf("expected [HOST/]OWNER/REPO")
	}
	if host == "" || strings.ContainsAny(host, " /\\\t\n\r?#") || !validRepoPart(parts[0]) || !validRepoPart(parts[1]) {
		return "", "", fmt.Errorf("invalid repository override")
	}
	return host, strings.Join(parts, "/"), nil
}

func remotePriority(name string) int {
	switch strings.ToLower(name) {
	case "upstream":
		return 3
	case "github":
		return 2
	case "origin":
		return 1
	}
	return 0
}

// Only simple Host/HostName configuration is interpreted. Anything that can
// dynamically alter host selection stays unresolved; we never execute ssh.
func sshHost(host, home string) (string, error) {
	if home == "" {
		return host, nil
	}
	data, err := readBounded(filepath.Join(home, ".ssh", "config"))
	if os.IsNotExist(err) {
		return host, nil
	}
	if err != nil {
		return "", fmt.Errorf("cannot read SSH config")
	}
	active, hostname := true, ""
	for _, line := range strings.Split(string(data), "\n") {
		text := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if text == "" {
			continue
		}
		text = strings.Replace(text, "=", " ", 1)
		fields := strings.Fields(text)
		if len(fields) < 2 {
			return "", fmt.Errorf("unsupported SSH config syntax")
		}
		key := strings.ToLower(fields[0])
		switch key {
		case "include", "match":
			return "", fmt.Errorf("SSH Include/Match host translation is unsupported")
		case "host":
			active = false
			excluded := false
			for _, pattern := range fields[1:] {
				neg := strings.HasPrefix(pattern, "!")
				pattern = strings.TrimPrefix(pattern, "!")
				match, err := path.Match(strings.ToLower(pattern), strings.ToLower(host))
				if err != nil {
					return "", fmt.Errorf("unsupported SSH Host pattern")
				}
				if match {
					if neg {
						excluded = true
					} else {
						active = true
					}
				}
			}
			active = active && !excluded
		case "hostname":
			if active && hostname == "" {
				if len(fields) != 2 {
					return "", fmt.Errorf("unsupported SSH HostName syntax")
				}
				hostname = strings.Trim(fields[1], "\"")
				hostname = strings.ReplaceAll(hostname, "%h", host)
				if strings.ContainsAny(hostname, "%${} /\\") {
					return "", fmt.Errorf("unsupported SSH HostName expansion")
				}
			}
		}
	}
	if hostname != "" {
		host = hostname
	}
	return normalizeHost(host), nil
}

func resolveLocal(cwd string, env map[string]string) localInfo {
	out := localInfo{context: model.ResolvedContext{Sources: make(map[string]string)}}
	c := &out.context
	unresolved := func(field, reason, source string) {
		c.Unresolved = append(c.Unresolved, model.Unresolved{Field: field, Reason: reason, Source: source})
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	gitdir, gitErr := gitDirectory(cwd, env)
	var entries []configEntry
	if gitErr == nil {
		c.GitDir = gitdir
		c.Sources["gitDir"] = "git"
		common := gitdir
		if data, err := readBounded(filepath.Join(gitdir, "commondir")); err == nil {
			if dir := strings.TrimSpace(string(data)); dir != "" {
				common = absolute(gitdir, dir)
			} else {
				gitErr = fmt.Errorf("empty git commondir")
			}
		} else if !os.IsNotExist(err) {
			gitErr = err
		}
		if data, err := readBounded(filepath.Join(gitdir, "HEAD")); err == nil {
			head := strings.TrimSpace(string(data))
			if strings.HasPrefix(head, "ref: ") {
				ref := strings.TrimSpace(head[5:])
				if strings.HasPrefix(ref, "refs/heads/") && path.Clean(ref) == ref && !strings.ContainsAny(ref, "\\ \t\r\n") && !strings.Contains(ref, "..") {
					c.Branch = strings.TrimPrefix(ref, "refs/heads/")
					c.Sources["branch"] = "git"
				}
				c.HeadSHA, err = readRef(gitdir, common, ref, 0)
				if err != nil {
					unresolved("target.sha", err.Error(), "git")
				}
			} else if validSHA(head) {
				c.HeadSHA = head
			} else {
				unresolved("target.sha", "invalid git HEAD", "git")
			}
			if c.HeadSHA != "" {
				c.Sources["headSha"] = "git"
			}
		} else {
			unresolved("target.sha", "cannot read git HEAD", "git")
		}
		if gitErr == nil {
			entries, gitErr = readConfig(filepath.Join(common, "config"), env["HOME"], 0)
		}
		for _, e := range entries {
			if e.section == "extensions" && e.key == "worktreeconfig" && strings.EqualFold(e.value, "true") {
				more, err := readConfig(filepath.Join(gitdir, "config.worktree"), env["HOME"], 0)
				if !os.IsNotExist(err) && err != nil {
					gitErr = err
				} else {
					entries = append(entries, more...)
				}
				break
			}
		}
	}
	for _, e := range entries {
		if e.section == "branch" && e.subsection == c.Branch && e.key == "gh-merge-base" {
			out.baseBranch = e.value
		}
	}
	if c.Branch == "" {
		unresolved("target.branch", "no current branch (detached HEAD or no repository)", "git")
	}
	fallback := normalizeHost(env["GH_HOST"])
	if fallback == "" {
		fallback = "github.com"
	}
	if override := env["GH_REPO"]; override != "" {
		host, repo, err := fullName(override, fallback)
		if err != nil {
			unresolved("target.repository", "invalid GH_REPO: "+err.Error(), "env")
		} else {
			c.Host, c.Repository = host, repo
			c.Owner, _, _ = strings.Cut(repo, "/")
			c.Sources["repository"], c.Sources["owner"], c.Sources["host"] = "env", "env", "env"
		}
	} else if gitErr != nil {
		unresolved("target.repository", gitErr.Error(), "git")
	} else {
		byName := make(map[string]*remote)
		for _, e := range entries {
			if e.section != "remote" {
				continue
			}
			rem := byName[e.subsection]
			if rem == nil {
				rem = &remote{name: e.subsection}
				byName[e.subsection] = rem
			}
			switch e.key {
			case "url":
				rem.fetch = e.value
			case "pushurl":
				rem.push = e.value
			case "gh-resolved":
				rem.resolved = e.value
			}
		}
		var remotes []*remote
		for _, rem := range byName {
			remotes = append(remotes, rem)
		}
		sort.Slice(remotes, func(i, j int) bool {
			a, b := remotePriority(remotes[i].name), remotePriority(remotes[j].name)
			if a != b {
				return a > b
			}
			return remotes[i].name < remotes[j].name
		})
		type candidate struct {
			remote                 *remote
			host, repo, hostReason string
		}
		var candidates []candidate
		for _, rem := range remotes {
			host, repo, ssh, err := parseRemote(rem.fetch)
			if err != nil {
				host, repo, ssh, err = parseRemote(rem.push)
			}
			if err != nil {
				continue
			}
			reason := ""
			if ssh {
				host, err = sshHost(host, env["HOME"])
				if err != nil {
					reason = err.Error()
				}
			}
			candidates = append(candidates, candidate{rem, host, repo, reason})
		}
		if len(candidates) == 0 {
			unresolved("target.repository", "no supported git remotes", "git")
		} else {
			chosen, marked := candidates[0], false
			for _, cand := range candidates {
				if cand.remote.resolved != "" {
					chosen, marked = cand, true
					break
				}
			}
			if marked && chosen.remote.resolved != "base" {
				_, repo, err := fullName(chosen.remote.resolved, fallback)
				if err != nil {
					unresolved("target.repository", "invalid gh-resolved repository", "git")
					chosen.repo = ""
				} else {
					chosen.repo = repo
				}
			}
			c.Host, c.Repository = chosen.host, chosen.repo
			c.Owner, _, _ = strings.Cut(chosen.repo, "/")
			if c.Repository != "" {
				c.Sources["repository"], c.Sources["owner"] = "git", "git"
			}
			if chosen.hostReason != "" {
				unresolved("target.host", chosen.hostReason, "git")
			} else if c.Host != "" {
				c.Sources["host"] = "git"
			}
			if len(candidates) > 1 && !marked {
				unresolved("target.repository", "multiple remotes; gh may prompt", "git")
			}
		}
	}
	// GH_HOST is only a fallback, not evidence for an unresolved SSH alias.
	if c.Host == "" && !hasUnresolved(c.Unresolved, "target.host") {
		c.Host = fallback
		if env["GH_HOST"] != "" {
			c.Sources["host"] = "env"
		} else {
			c.Sources["host"] = "default"
		}
	}
	return out
}

func hasUnresolved(fields []model.Unresolved, name string) bool {
	for _, field := range fields {
		if field.Field == name {
			return true
		}
	}
	return false
}

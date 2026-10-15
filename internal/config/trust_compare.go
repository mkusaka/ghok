package config

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mkusaka/ghok/internal/gitmeta"
)

// Repository provenance only selects a review baseline. It never grants trust
// or participates in the content digest, including when metadata is unavailable.
func currentTrustEntry(project, canonical string, files []trustedFile, env map[string]string) trustEntry {
	entry := trustEntry{ManifestVersion: 1, FirstPath: canonical, ConfigPath: ".ghok.yaml", Files: files}
	root := filepath.Dir(project)
	// Trust is for this project, not a repository redirected by GIT_DIR.
	gitdir, err := gitmeta.Directory(root, nil)
	if err != nil {
		return entry
	}
	common := gitdir
	if data, err := gitmeta.ReadBounded(filepath.Join(gitdir, "commondir")); err == nil {
		if value := strings.TrimSpace(string(data)); value != "" {
			common = gitmeta.Absolute(gitdir, value)
		} else {
			return entry
		}
	}
	entry.CommonDir, _ = filepath.EvalSymlinks(common)
	if data, err := gitmeta.ReadBounded(filepath.Join(gitdir, "HEAD")); err == nil {
		head := strings.TrimSpace(string(data))
		if ref, ok := strings.CutPrefix(head, "ref: "); ok {
			entry.Commit, _ = gitmeta.ReadRef(gitdir, common, strings.TrimSpace(ref), 0)
		} else if gitmeta.ValidSHA(head) {
			entry.Commit = strings.ToLower(head)
		}
	}
	entries, _ := gitmeta.ReadConfig(filepath.Join(common, "config"), env["HOME"], 0)
	for _, e := range entries {
		if e.Section == "extensions" && e.Key == "worktreeconfig" && strings.EqualFold(e.Value, "true") {
			more, _ := gitmeta.ReadConfig(filepath.Join(gitdir, "config.worktree"), env["HOME"], 0)
			entries = append(entries, more...)
			break
		}
	}
	for _, e := range entries {
		if e.Section == "remote" && e.Subsection == "origin" && e.Key == "url" {
			entry.RemoteURL = normalizeTrustRemote(e.Value, root)
		}
	}
	return entry
}

// Normalize transport spelling without interpreting insteadOf rules. Repository
// paths remain case-sensitive; only the host is case-insensitive.
func normalizeTrustRemote(raw, root string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		if colon := strings.IndexByte(raw, ':'); colon >= 0 && !strings.ContainsAny(raw[:colon], "/\\") {
			raw = "ssh://" + raw[:colon] + "/" + raw[colon+1:]
		} else {
			local := gitmeta.Absolute(root, raw)
			if resolved, err := filepath.EvalSymlinks(local); err == nil {
				local = resolved
			}
			return "file://" + filepath.ToSlash(local)
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme == "file" && (u.Host == "" || u.Host == "localhost") {
		return normalizeTrustRemote(u.Path, root)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	port := u.Port()
	if port == "22" || port == "443" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if path == "" {
		return ""
	}
	return host + "/" + path
}

func previousTrustEntry(current trustEntry, store map[string]trustEntry) *trustEntry {
	hashes := make(map[string]bool, len(current.Files))
	for _, file := range current.Files {
		if !file.Missing {
			hashes[file.SHA256] = true
		}
	}
	var latest *trustEntry
	bestTier, bestKey := 5, ""
	for key, entry := range store {
		tier := 5
		switch {
		case current.CommonDir != "" && entry.CommonDir == current.CommonDir:
			tier = 1
		case entry.FirstPath == current.FirstPath:
			tier = 2
		case current.RemoteURL != "" && entry.RemoteURL == current.RemoteURL:
			tier = 3
		default:
			shared := make(map[string]bool)
			for _, file := range entry.Files {
				if !file.Missing && hashes[file.SHA256] {
					shared[file.SHA256] = true
				}
			}
			if len(shared) >= 2 {
				tier = 4
			}
		}
		if tier < bestTier || (tier == bestTier && tier < 5 && (entry.TrustedAt.After(latest.TrustedAt) || (entry.TrustedAt.Equal(latest.TrustedAt) && key < bestKey))) {
			latest, bestTier, bestKey = &entry, tier, key
		}
	}
	return latest
}

func trustComparisonNotes(current trustEntry, store map[string]trustEntry) ([]string, *trustEntry) {
	previous := previousTrustEntry(current, store)
	if previous == nil {
		return []string{"no previous trust record for this repository"}, nil
	}
	notes := []string{fmt.Sprintf("Previous trust: trustedAt: %s; path: %s", previous.TrustedAt.Format(time.RFC3339), previous.FirstPath)}
	before := make(map[string]trustedFile, len(previous.Files))
	for _, file := range previous.Files {
		before[file.Path] = file
	}
	var changed, added, removed []string
	for _, file := range current.Files {
		old, exists := before[file.Path]
		switch {
		case !exists || (old.Missing && !file.Missing):
			added = append(added, file.Path)
		case !old.Missing && file.Missing:
			removed = append(removed, file.Path)
		case old.SHA256 != file.SHA256 || old.Executable != file.Executable:
			if old.Executable != file.Executable && !old.Missing && !file.Missing {
				mode := func(executable bool) string {
					if executable {
						return "0755"
					}
					return "0644"
				}
				notes = append(notes, fmt.Sprintf("%s: mode %s → %s", file.Path, mode(old.Executable), mode(file.Executable)))
			}
			changed = append(changed, file.Path)
		}
		delete(before, file.Path)
	}
	for path := range before {
		removed = append(removed, path)
	}
	for _, group := range []struct {
		name  string
		paths []string
	}{{"Changed", changed}, {"Added", added}, {"Removed", removed}} {
		sort.Strings(group.paths)
		list := "none"
		if len(group.paths) > 0 {
			list = strings.Join(group.paths, ", ")
		}
		notes = append(notes, group.name+" files since trust: "+list)
	}
	return notes, previous
}

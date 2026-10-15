package resolve

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mkusaka/ghok/internal/config"
)

const (
	nodeCacheTTL      = 24 * time.Hour
	nodeCacheLimit    = 5000
	nodeCacheMaxBytes = 4 << 20
)

type nodeEntry struct {
	Kind       string    `json:"kind"`
	Repository string    `json:"repository"`
	Number     int       `json:"number,omitempty"`
	Host       string    `json:"host"`
	FetchedAt  time.Time `json:"fetchedAt"`
}

func (r *Resolver) nodeCachePath() string {
	// A deliberately isolated environment without a home/config directory must
	// not create state relative to the invocation's working directory.
	if r.env["GHOK_CONFIG_DIR"] == "" && r.env["XDG_CONFIG_HOME"] == "" && r.env["HOME"] == "" {
		return ""
	}
	return filepath.Join(config.Dir(r.env), "state", "nodes.json")
}

func validNodeEntry(entry nodeEntry, now time.Time) bool {
	if entry.Host == "" || entry.FetchedAt.IsZero() || entry.FetchedAt.After(now) || now.Sub(entry.FetchedAt) >= nodeCacheTTL {
		return false
	}
	if _, _, err := fullName(entry.Repository, entry.Host); err != nil {
		return false
	}
	switch entry.Kind {
	case "pullRequest", "issue":
		return entry.Number > 0
	case "repository", "ref", "comment":
		return entry.Number == 0
	default:
		return false
	}
}

func (r *Resolver) loadNodes() {
	if r.nodesLoaded {
		return
	}
	r.nodesLoaded = true
	r.nodes = make(map[string]nodeEntry)
	path := r.nodeCachePath()
	if path == "" {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > nodeCacheMaxBytes {
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, nodeCacheMaxBytes+1))
	if err != nil || len(data) > nodeCacheMaxBytes {
		return
	}
	var entries map[string]nodeEntry
	if json.Unmarshal(data, &entries) != nil {
		return
	}
	now := time.Now()
	for id, entry := range entries {
		if id != "" && validNodeEntry(entry, now) {
			r.nodes[id] = entry
		}
	}
	r.trimNodes()
}

func (r *Resolver) cachedNode(host, id string) (nodeResult, bool) {
	r.loadNodes()
	entry, ok := r.nodes[id]
	if !ok || !strings.EqualFold(entry.Host, host) || !validNodeEntry(entry, time.Now()) {
		return nodeResult{}, false
	}
	return nodeResult{Kind: entry.Kind, Repository: entry.Repository, Number: entry.Number}, true
}

func (r *Resolver) trimNodes() {
	now := time.Now()
	for id, entry := range r.nodes {
		if !validNodeEntry(entry, now) {
			delete(r.nodes, id)
		}
	}
	if len(r.nodes) <= nodeCacheLimit {
		return
	}
	ids := make([]string, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		if order := r.nodes[a].FetchedAt.Compare(r.nodes[b].FetchedAt); order != 0 {
			return order
		}
		return strings.Compare(a, b)
	})
	for _, id := range ids[:len(ids)-nodeCacheLimit] {
		delete(r.nodes, id)
	}
}

func (r *Resolver) storeNode(host, id string, node nodeResult) {
	r.loadNodes()
	r.nodes[id] = nodeEntry{Kind: node.Kind, Repository: node.Repository, Number: node.Number, Host: host, FetchedAt: time.Now().UTC()}
	r.trimNodes()
	path := r.nodeCachePath()
	if path == "" {
		return
	}
	data, err := json.Marshal(r.nodes)
	if err != nil || len(data) > nodeCacheMaxBytes || os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".nodes-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr == nil && closeErr == nil {
		_ = os.Rename(file.Name(), path)
	}
}

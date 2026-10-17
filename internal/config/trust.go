package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// IgnoredHook is reporting metadata, never an executable hook selection.
type IgnoredHook struct {
	Hook
	Transform    bool     `json:"transform,omitempty"`
	Reason       string   `json:"reason"`
	ChangedFiles []string `json:"changedFiles,omitempty"`
	Digest       string   `json:"digest,omitempty"`
	GateBlocked  bool     `json:"gateBlocked,omitempty"`
}

const maxTrustFiles = 256

type trustedFile struct {
	Path       string   `json:"path"`
	SHA256     string   `json:"sha256,omitempty"`
	Executable bool     `json:"executable"`
	Missing    bool     `json:"missing,omitempty"`
	HookIDs    []string `json:"hookIds,omitempty"`
}

type trustEntry struct {
	ManifestVersion int               `json:"manifestVersion"`
	FirstPath       string            `json:"firstPath"`
	ConfigPath      string            `json:"configPath"`
	Commit          string            `json:"commit,omitempty"`
	CommonDir       string            `json:"commonDir,omitempty"`
	RemoteURL       string            `json:"remoteUrl,omitempty"`
	TrustedAt       time.Time         `json:"trustedAt"`
	HookIDs         []string          `json:"hookIds"`
	HookDigests     map[string]string `json:"hookDigests"`
	// Digests are earlier grants kept so that worktrees on other branches
	// stay trusted after a newer version of a hook is trusted.
	Digests     []string                   `json:"digests,omitempty"`
	Definitions map[string]json.RawMessage `json:"definitions"`
	Files       []trustedFile              `json:"files"`
}

func contentHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// resolveTrustPath also resolves symlinks in missing paths, including dangling
// links. A missing target outside the repository must not be trusted as local.
func resolveTrustPath(path string, links int) (string, error) {
	if links > 255 {
		return "", errors.New("too many hook path symlinks")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return resolved, err
	}
	info, statErr := os.Lstat(path)
	if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		return resolveTrustPath(target, links+1)
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	parent, err := resolveTrustPath(filepath.Dir(path), links)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func trustRelative(root, resolved string) (string, error) {
	relative, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(relative) {
		return "", errors.New("path escapes repository root")
	}
	return filepath.ToSlash(relative), nil
}

// readProjectData rejects special files before opening them and bounds the YAML
// read even when the project has no hooks and needs no trust manifest.
func readProjectData(project string, maxBytes int64) ([]byte, string, error) {
	resolved, err := filepath.EvalSymlinks(project)
	if err != nil {
		return nil, "", err
	}
	if _, err := trustRelative(filepath.Dir(project), resolved); err != nil {
		return nil, "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() {
		return nil, "", errors.New("project config must be a regular file")
	}
	if info.Size() > maxBytes {
		return nil, "", errors.New("project config exceeds runtime.fileMaxBytes")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		return nil, "", err
	}
	var extra [1]byte
	if n, err := file.Read(extra[:]); n != 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return nil, "", errors.New("project config exceeds runtime.fileMaxBytes or is unreadable")
	}
	return data, resolved, nil
}

// hookDefinition uses the parsed, defaulted values, never YAML presentation or
// load-time provenance. encoding/json sorts map keys deterministically.
func hookDefinition(h Hook) json.RawMessage {
	h.Source = ""
	data, _ := json.Marshal(h)
	return data
}

func hookTrustDigest(definition json.RawMessage, files []trustedFile, id string) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("hook-v2\x00"))
	_, _ = digest.Write(definition)
	for _, file := range files {
		if !slices.Contains(file.HookIDs, id) {
			continue
		}
		_, _ = fmt.Fprintf(digest, "\x00%s\x00%t\x00%t\x00%s", file.Path, file.Executable, file.Missing, file.SHA256)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func projectManifest(p *Config, root string, maxBytes int64, env map[string]string, readOnly bool) ([]trustedFile, error) {
	var files []trustedFile
	byPath := make(map[string]int)
	total := int64(0)
	cache := readManifestCache(env)
	add := func(path, hookID string, externalOK bool) error {
		resolved, err := resolveTrustPath(path, 0)
		if err != nil {
			return err
		}
		relative, err := trustRelative(root, resolved)
		if err != nil {
			if externalOK {
				// An absolute external command is not repository-supplied code.
				return nil
			}
			return err
		}
		if index, ok := byPath[relative]; ok {
			if !slices.Contains(files[index].HookIDs, hookID) {
				files[index].HookIDs = append(files[index].HookIDs, hookID)
			}
			return nil
		}
		if len(files) == maxTrustFiles {
			return fmt.Errorf("trust manifest exceeds %d files", maxTrustFiles)
		}
		file := trustedFile{Path: relative}
		if hookID != "" {
			file.HookIDs = []string{hookID}
		}
		info, err := os.Stat(resolved)
		if errors.Is(err, os.ErrNotExist) {
			file.Missing = true
		} else {
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return errors.New("trusted path must be a regular file; list individual files instead of directories")
			}
			file.Executable = info.Mode().Perm()&0o111 != 0
			remaining := maxBytes - total
			if info.Size() > remaining {
				return errors.New("trust manifest exceeds runtime.fileMaxBytes")
			}
			if hash, ok := cache.lookup(resolved, info); ok {
				total += info.Size()
				file.SHA256 = hash
			} else {
				input, err := os.Open(resolved)
				if err != nil {
					return err
				}
				hash := sha256.New()
				// Avoid overflowing a MaxInt64 configured byte limit.
				n, readErr := io.Copy(hash, io.LimitReader(input, remaining))
				var extra [1]byte
				if nExtra, extraErr := input.Read(extra[:]); nExtra != 0 || (extraErr != nil && !errors.Is(extraErr, io.EOF)) {
					_ = input.Close()
					return errors.New("trust manifest exceeds runtime.fileMaxBytes or is unreadable")
				}
				after, statErr := input.Stat()
				closeErr := input.Close()
				if readErr != nil {
					return readErr
				}
				if closeErr != nil {
					return closeErr
				}
				total += n
				if total > maxBytes {
					return errors.New("trust manifest exceeds runtime.fileMaxBytes")
				}
				file.SHA256 = hex.EncodeToString(hash.Sum(nil))
				if statErr == nil && n == info.Size() {
					cache.remember(resolved, info, after, file.SHA256)
				}
			}
		}
		byPath[relative] = len(files)
		files = append(files, file)
		return nil
	}
	for _, h := range p.trustHooks() {
		command := h.Command[0]
		absolute := filepath.IsAbs(command)
		path := command
		if !absolute {
			path = filepath.Join(root, command)
		}
		_, lexicalErr := trustRelative(root, path)
		if err := add(path, h.ID, absolute && lexicalErr != nil); err != nil {
			return nil, fmt.Errorf("hook %s: %w", h.ID, err)
		}
		for _, path := range h.Files {
			if err := add(filepath.Join(root, path), h.ID, false); err != nil {
				return nil, fmt.Errorf("hook %s files %q: %w", h.ID, path, err)
			}
		}
	}
	for _, path := range p.Trust.Include {
		for _, h := range p.trustHooks() {
			if err := add(filepath.Join(root, path), h.ID, false); err != nil {
				return nil, fmt.Errorf("trust.include %q: %w", path, err)
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for i := range files {
		sort.Strings(files[i].HookIDs)
	}
	if !readOnly {
		cache.save()
	}
	return files, nil
}

func validDigest(value string) bool {
	digest, err := hex.DecodeString(value)
	return err == nil && len(digest) == sha256.Size && strings.ToLower(value) == value
}

func trustStorePath(env map[string]string) string {
	return filepath.Join(Dir(env), "trust.json")
}

func parseTrustStore(data []byte) (map[string]trustEntry, []string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}
	if raw == nil {
		return nil, nil, errors.New("trust store must be a JSON object")
	}
	store := make(map[string]trustEntry, len(raw))
	var notes []string
	for key, value := range raw {
		var entry trustEntry
		err := json.Unmarshal(value, &entry)
		// MarshalIndent formats RawMessage values in the store. Restore the
		// canonical compact definition before verifying its digest.
		if err == nil {
			for id, definition := range entry.Definitions {
				var compact bytes.Buffer
				if err = json.Compact(&compact, definition); err != nil {
					break
				}
				entry.Definitions[id] = compact.Bytes()
			}
		}
		if err == nil {
			err = validateTrustEntry(key, entry)
		}
		if err != nil {
			name := key
			if len(name) > 12 {
				name = name[:12] + "…"
			}
			notes = append(notes, fmt.Sprintf("Trust store: skipped malformed trust entry %q (%v)", name, err))
			continue
		}
		store[key] = entry
	}
	sort.Strings(notes)
	return store, notes, nil
}

func validateTrustEntry(key string, entry trustEntry) error {
	if !validDigest(key) || (key != trustKey(entry) && key != contentHash([]byte(entry.FirstPath))) || entry.ManifestVersion != 2 || !filepath.IsAbs(entry.FirstPath) || filepath.Clean(entry.FirstPath) != entry.FirstPath || entry.ConfigPath != ".ghok.yaml" || entry.TrustedAt.IsZero() || len(entry.HookDigests) == 0 {
		return errors.New("invalid trust entry")
	}
	for _, file := range entry.Files {
		if !filepath.IsLocal(file.Path) || filepath.ToSlash(filepath.Clean(file.Path)) != file.Path || (file.Missing && (file.SHA256 != "" || file.Executable)) || (!file.Missing && !validDigest(file.SHA256)) {
			return errors.New("invalid trusted file")
		}
	}
	for _, digest := range entry.Digests {
		if !validDigest(digest) {
			return errors.New("invalid trusted digest")
		}
	}
	for id, digest := range entry.HookDigests {
		if !hookID.MatchString(id) || !validDigest(digest) || !json.Valid(entry.Definitions[id]) || hookTrustDigest(entry.Definitions[id], entry.Files, id) != digest {
			return errors.New("invalid hook digest")
		}
	}
	return nil
}

// Invalid top-level JSON grants no trust; malformed individual entries are isolated.
func readTrustStore(env map[string]string) (map[string]trustEntry, []string) {
	path := trustStorePath(env)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]trustEntry), nil
	}
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("trust store must be a regular file, not a symlink")
	}
	var store map[string]trustEntry
	var notes []string
	if err == nil {
		var data []byte
		data, err = os.ReadFile(path)
		if err == nil {
			store, notes, err = parseTrustStore(data)
		}
	}
	if err != nil {
		return make(map[string]trustEntry), []string{fmt.Sprintf("Trust store: %s is invalid or unreadable (%v); no project hooks trusted; run ghok config trust", path, err)}
	}
	return store, notes
}

func writeTrustStore(env map[string]string, store map[string]trustEntry) error {
	path := trustStorePath(env)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".trust-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	// CreateTemp uses 0600; explicit chmod also handles restrictive umasks.
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// trustProjectPath retains the repository directory for relative hook commands.
func trustProjectPath(path, cwd string, env map[string]string) (string, error) {
	if path == "" {
		return ProjectPath(cwd, env)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	project, err := ProjectPath(filepath.Dir(path), env)
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	if filepath.Join(dir, filepath.Base(path)) != project {
		return "", fmt.Errorf("%s is not the repository-root .ghok.yaml (%s)", path, project)
	}
	return project, nil
}

// TrustProject validates and reports the exact content being authorized without
// running hooks. dryRun skips all state writes; noDiff skips raw content output.
// The byte result is an unredacted review diff, separate from diagnostic notes.
func TrustProject(path, cwd string, env map[string]string, dryRun, noDiff bool, hookIDs ...string) (string, []Hook, []string, []byte, error) {
	project, err := trustProjectPath(path, cwd, env)
	if err != nil {
		return "", nil, nil, nil, err
	}
	var notes []string
	c, err := load(DefaultPath(env), &notes)
	if err != nil {
		return "", nil, notes, nil, err
	}
	data, canonical, err := readProjectData(project, c.Runtime.FileMaxBytes)
	if err != nil {
		return "", nil, notes, nil, err
	}
	p, n, err := parseProject(data, project)
	if err != nil {
		return "", nil, nil, nil, err
	}
	notes = append(notes, projectUnknownKeys(n, project)...)
	var files []trustedFile
	if len(p.Hooks)+len(p.Transforms) != 0 {
		files, err = projectManifest(p, filepath.Dir(project), c.Runtime.FileMaxBytes, env, dryRun)
		if err != nil {
			return "", nil, notes, nil, err
		}
	}
	current := currentTrustEntry(project, canonical, p, files, env)
	selected := make(map[string]bool, len(p.Hooks)+len(p.Transforms))
	for _, id := range hookIDs {
		if _, exists := current.HookDigests[id]; !exists {
			return "", nil, notes, nil, fmt.Errorf("unknown project hook %q", id)
		}
		selected[id] = true
	}
	if len(hookIDs) == 0 {
		for _, h := range p.trustHooks() {
			selected[h.ID] = true
		}
	}
	if err := mergeProject(c, p, n, project, filepath.Dir(project), env, &notes); err != nil {
		return "", nil, notes, nil, err
	}
	if len(p.Hooks)+len(p.Transforms) == 0 {
		if !dryRun {
			store, _ := readTrustStore(env)
			if _, err := prepareTrustBlobs(env, store); err != nil {
				return "", nil, notes, nil, err
			}
		}
		return canonical, nil, append(notes, "No project hooks; no trust manifest stored."), nil, nil
	}
	store, storeNotes := readTrustStore(env)
	notes = append(notes, storeNotes...)
	key := trustKey(current)
	previous := previousTrustEntry(current, store)
	if exact, exists := store[key]; exists {
		previous = &exact
	}
	review, diff := hookTrustReview(project, current, previous, selected, env, noDiff)
	notes = append(notes, review...)
	if !dryRun {
		prior := store[key]
		trusted := current
		if len(hookIDs) != 0 {
			trusted = mergeSelectedTrust(current, prior, selected)
		}
		trusted.Digests = grantHistory(prior, trusted)
		trusted.TrustedAt = time.Now().UTC()
		store[key] = trusted
		blobNotes, err := saveTrustContents(project, selectedTrustFiles(files, selected), env, store)
		notes = append(notes, blobNotes...)
		if err != nil {
			return "", nil, notes, diff, err
		}
		if err := writeTrustStore(env, store); err != nil {
			return "", nil, notes, diff, err
		}
	}
	notes = append(notes, trustFileNotes(selectedTrustFiles(files, selected))...)
	for _, h := range p.Hooks {
		if selected[h.ID] && h.Phase == PhasePre && h.OnError == OnErrorDeny && !h.ContinueOnError {
			for _, file := range filesForHook(files, h.ID) {
				if file.Missing {
					notes = append(notes, fmt.Sprintf("Warning: gate hook %s dependency file %s is missing", h.ID, file.Path))
				}
			}
		}
	}
	notes = append(notes, "Not covered: interpreters, PATH tools, and unlisted data/import files. Add dependencies to hooks[].files or trust.include (every hook).")
	var active []Hook
	for _, h := range p.trustHooks() {
		if selected[h.ID] && (h.Enabled == nil || *h.Enabled) {
			active = append(active, h)
		}
	}
	return canonical, active, notes, diff, nil
}

// UntrustProject removes hook authorization without changing declarative policy.
// A missing trust entry is success; a missing project file is an error.
func UntrustProject(path, cwd string, env map[string]string) (string, []string, error) {
	project, err := trustProjectPath(path, cwd, env)
	if err != nil {
		return "", nil, err
	}
	c, err := load(DefaultPath(env), nil)
	if err != nil {
		return "", nil, err
	}
	data, canonical, err := readProjectData(project, c.Runtime.FileMaxBytes)
	if err != nil {
		return "", nil, err
	}
	p, _, err := parseProject(data, project)
	if err != nil {
		return "", nil, err
	}
	store, notes := readTrustStore(env)
	key := trustKey(currentTrustEntry(project, canonical, p, nil, env))
	if _, exists := store[key]; !exists {
		_, err := prepareTrustBlobs(env, store)
		return canonical, notes, err
	}
	delete(store, key)
	if err := writeTrustStore(env, store); err != nil {
		return "", notes, err
	}
	_, err = prepareTrustBlobs(env, store)
	return canonical, notes, err
}

func trustFileNotes(files []trustedFile) []string {
	notes := make([]string, 0, len(files))
	for _, file := range files {
		status, executable := "sha256:"+file.SHA256, 0
		if file.Missing {
			status = "missing"
		}
		if file.Executable {
			executable = 1
		}
		hooks := ""
		if len(file.HookIDs) != 0 {
			hooks = " (hooks: " + strings.Join(file.HookIDs, ", ") + ")"
		}
		notes = append(notes, fmt.Sprintf("Hashed file: %s exec:%d %s%s", file.Path, executable, status, hooks))
	}
	return notes
}

// ListProjectTrust reports each project's per-hook grants without loading a repo.
func ListProjectTrust(env map[string]string) []string {
	store, notes := readTrustStore(env)
	keys := make([]string, 0, len(store))
	for key := range store {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := store[key]
		notes = append(notes, "Project record: "+key, "firstPath: "+entry.FirstPath,
			"trustedAt: "+entry.TrustedAt.Format(time.RFC3339), "hook IDs: "+strings.Join(entry.HookIDs, ", "))
		notes = append(notes, "commit: "+entry.Commit, "commonDir: "+entry.CommonDir, "remoteUrl: "+entry.RemoteURL)
		for _, id := range entry.HookIDs {
			notes = append(notes, "hook "+id+" trust digest: "+entry.HookDigests[id])
		}
		notes = append(notes, trustFileNotes(entry.Files)...)
	}
	if len(store) == 0 {
		notes = append(notes, "No trusted project digests.")
	}
	return notes
}

// UntrustAllProjects clears every content grant, independent of the current repo.
func UntrustAllProjects(env map[string]string) ([]string, error) {
	_, notes := readTrustStore(env)
	store := make(map[string]trustEntry)
	if err := writeTrustStore(env, store); err != nil {
		return notes, err
	}
	_, err := prepareTrustBlobs(env, store)
	return notes, err
}

func changedTrustFiles(files, previous []trustedFile) []string {
	before := make(map[string]trustedFile, len(previous))
	for _, file := range previous {
		before[file.Path] = file
	}
	var changed []string
	for _, file := range files {
		old, exists := before[file.Path]
		if !exists || file.SHA256 != old.SHA256 || file.Missing != old.Missing || file.Executable != old.Executable {
			changed = append(changed, file.Path)
		}
		delete(before, file.Path)
	}
	for path := range before {
		changed = append(changed, path)
	}
	sort.Strings(changed)
	return changed
}

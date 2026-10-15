package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// IgnoredHook is reporting metadata, never an executable hook selection.
type IgnoredHook struct {
	Hook
	Reason string `json:"reason"`
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
	ManifestVersion int           `json:"manifestVersion"`
	FirstPath       string        `json:"firstPath"`
	ConfigPath      string        `json:"configPath"`
	Commit          string        `json:"commit,omitempty"`
	CommonDir       string        `json:"commonDir,omitempty"`
	RemoteURL       string        `json:"remoteUrl,omitempty"`
	TrustedAt       time.Time     `json:"trustedAt"`
	HookIDs         []string      `json:"hookIds"`
	Files           []trustedFile `json:"files"`
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

func manifestDigest(files []trustedFile) string {
	digest := sha256.New()
	_, _ = io.WriteString(digest, "v1\n")
	for _, file := range files {
		executable, hash := "0", file.SHA256
		if file.Executable {
			executable = "1"
		}
		if file.Missing {
			hash = "missing"
		}
		_, _ = io.WriteString(digest, file.Path+"\x00"+executable+"\x00"+hash+"\n")
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func projectManifest(data []byte, p *Config, root string, maxBytes int64, env map[string]string, readOnly bool) (string, []trustedFile, error) {
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
			if hookID != "" {
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
				return errors.New("trusted path must be a regular file")
			}
			file.Executable = info.Mode().Perm()&0o111 != 0
			remaining := maxBytes - total
			if info.Size() > remaining {
				return errors.New("trust manifest exceeds runtime.fileMaxBytes")
			}
			if len(files) == 0 {
				total += int64(len(data))
				if total > maxBytes {
					return errors.New("trust manifest exceeds runtime.fileMaxBytes")
				}
				file.SHA256 = contentHash(data)
			} else if hash, ok := cache.lookup(resolved, info); ok {
				total += info.Size()
				file.SHA256 = hash
			} else {
				input, err := os.Open(resolved)
				if err != nil {
					return err
				}
				hash := sha256.New()
				// The YAML has already consumed at least one byte, so +1
				// cannot overflow, even with a MaxInt64 user limit.
				n, readErr := io.Copy(hash, io.LimitReader(input, remaining+1))
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
	if err := add(filepath.Join(root, ".ghok.yaml"), "", false); err != nil {
		return "", nil, fmt.Errorf(".ghok.yaml: %w", err)
	}
	for _, h := range p.Hooks {
		command := h.Command[0]
		absolute := filepath.IsAbs(command)
		path := command
		if !absolute {
			path = filepath.Join(root, command)
		}
		_, lexicalErr := trustRelative(root, path)
		if err := add(path, h.ID, absolute && lexicalErr != nil); err != nil {
			return "", nil, fmt.Errorf("hook %s: %w", h.ID, err)
		}
	}
	for _, path := range p.Trust.Include {
		if err := add(filepath.Join(root, path), "", false); err != nil {
			return "", nil, fmt.Errorf("trust.include %q: %w", path, err)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for i := range files {
		sort.Strings(files[i].HookIDs)
	}
	if !readOnly {
		cache.save()
	}
	return manifestDigest(files), files, nil
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
	if !validDigest(key) || entry.ManifestVersion != 1 || !filepath.IsAbs(entry.FirstPath) || filepath.Clean(entry.FirstPath) != entry.FirstPath || entry.ConfigPath != ".ghok.yaml" || entry.TrustedAt.IsZero() || len(entry.Files) == 0 || len(entry.Files) > maxTrustFiles {
		return errors.New("invalid trust entry")
	}
	for i, file := range entry.Files {
		if !filepath.IsLocal(file.Path) || filepath.ToSlash(filepath.Clean(file.Path)) != file.Path || (file.Missing && (file.SHA256 != "" || file.Executable)) || (!file.Missing && !validDigest(file.SHA256)) || (i > 0 && entry.Files[i-1].Path >= file.Path) {
			return errors.New("invalid trusted file")
		}
	}
	if manifestDigest(entry.Files) != key {
		return errors.New("invalid manifest digest")
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
func TrustProject(path, cwd string, env map[string]string, dryRun, noDiff bool) (string, []Hook, []string, []byte, error) {
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
	var digest string
	var files []trustedFile
	if len(p.Hooks) != 0 {
		digest, files, err = projectManifest(data, p, filepath.Dir(project), c.Runtime.FileMaxBytes, env, dryRun)
		if err != nil {
			return "", nil, notes, nil, err
		}
	}
	if err := mergeProject(c, p, n, project, filepath.Dir(project), env, "", &notes); err != nil {
		return "", nil, notes, nil, err
	}
	if len(p.Hooks) == 0 {
		if !dryRun {
			store, _ := readTrustStore(env)
			if _, err := prepareTrustBlobs(env, store); err != nil {
				return "", nil, notes, nil, err
			}
		}
		return canonical, nil, append(notes, "No project hooks; no trust manifest stored."), nil, nil
	}
	hookIDs := make([]string, 0, len(p.Hooks))
	for _, h := range p.Hooks {
		hookIDs = append(hookIDs, h.ID)
	}
	sort.Strings(hookIDs)
	store, storeNotes := readTrustStore(env)
	notes = append(notes, storeNotes...)
	current := currentTrustEntry(project, canonical, files, env)
	comparison, previous := trustComparisonNotes(current, store)
	notes = append(notes, comparison...)
	var diff []byte
	if !noDiff {
		diff = trustContentDiff(project, current, previous, env)
	}
	if !dryRun {
		if previous, exists := store[digest]; exists {
			current.FirstPath = previous.FirstPath
		}
		current.TrustedAt, current.HookIDs = time.Now().UTC(), hookIDs
		store[digest] = current
		blobNotes, err := saveTrustContents(project, files, env, store)
		notes = append(notes, blobNotes...)
		if err != nil {
			return "", nil, notes, diff, err
		}
		if err := writeTrustStore(env, store); err != nil {
			return "", nil, notes, diff, err
		}
	}
	notes = append(notes, "Trust digest: "+digest)
	notes = append(notes, trustFileNotes(files)...)
	notes = append(notes, "Not covered: interpreters, PATH tools, and unlisted data/import files. Add repository dependencies to trust.include.")
	active := p.Hooks[:0]
	for _, h := range p.Hooks {
		if h.Enabled == nil || *h.Enabled {
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
	if len(p.Hooks) == 0 {
		store, notes := readTrustStore(env)
		_, err := prepareTrustBlobs(env, store)
		return canonical, notes, err
	}
	digest, _, err := projectManifest(data, p, filepath.Dir(project), c.Runtime.FileMaxBytes, env, false)
	if err != nil {
		return "", nil, err
	}
	store, notes := readTrustStore(env)
	if _, exists := store[digest]; !exists {
		_, err := prepareTrustBlobs(env, store)
		return canonical, notes, err
	}
	delete(store, digest)
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

// ListProjectTrust reports every trusted content digest without loading a repo.
func ListProjectTrust(env map[string]string) []string {
	store, notes := readTrustStore(env)
	keys := make([]string, 0, len(store))
	for key := range store {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := store[key]
		notes = append(notes, "Trust digest: "+key, "firstPath: "+entry.FirstPath,
			"trustedAt: "+entry.TrustedAt.Format(time.RFC3339), "hook IDs: "+strings.Join(entry.HookIDs, ", "))
		notes = append(notes, "commit: "+entry.Commit, "commonDir: "+entry.CommonDir, "remoteUrl: "+entry.RemoteURL)
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

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mkusaka/ghok/internal/textdiff"
)

var errTrustCopyTooLarge = errors.New("no stored copy (over 1 MiB)")

// Recheck containment and the manifest hash: a file replaced after hashing must
// not become a stored baseline for different bytes.
func readTrustContents(root string, file trustedFile) ([]byte, error) {
	resolved, err := resolveTrustPath(filepath.Join(root, filepath.FromSlash(file.Path)), 0)
	if err != nil {
		return nil, err
	}
	if _, err := trustRelative(root, resolved); err != nil {
		return nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("trusted path must be a regular file")
	}
	if info.Size() > maxTrustBlobBytes {
		return nil, errTrustCopyTooLarge
	}
	input, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer func() { _ = input.Close() }()
	data, err := io.ReadAll(io.LimitReader(input, maxTrustBlobBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTrustBlobBytes {
		return nil, errTrustCopyTooLarge
	}
	if contentHash(data) != file.SHA256 {
		return nil, errors.New("file changed while reviewing trust; retry")
	}
	return data, nil
}

func saveTrustContents(project string, files []trustedFile, env map[string]string, store map[string]trustEntry) ([]string, error) {
	total, err := prepareTrustBlobs(env, store)
	if err != nil {
		return nil, err
	}
	var notes []string
	for _, file := range files {
		if file.Missing {
			continue
		}
		data, err := readTrustContents(filepath.Dir(project), file)
		if errors.Is(err, errTrustCopyTooLarge) {
			notes = append(notes, file.Path+": "+err.Error())
			continue
		}
		if err != nil {
			return notes, fmt.Errorf("store trusted contents %s: %w", file.Path, err)
		}
		note, err := storeTrustBlob(env, file.SHA256, data, &total)
		if err != nil {
			return notes, err
		}
		if note != "" {
			notes = append(notes, file.Path+": "+note)
		}
	}
	return notes, nil
}

// Review bytes deliberately bypass diagnostic redaction. A redacted diff would
// misrepresent the trusted baseline. Only explicit trust commands render it.
func trustContentDiff(project string, current trustEntry, previous *trustEntry, env map[string]string) []byte {
	if previous == nil {
		return nil
	}
	before, after := make(map[string]trustedFile), make(map[string]trustedFile)
	for _, file := range previous.Files {
		before[file.Path] = file
	}
	for _, file := range current.Files {
		after[file.Path] = file
	}
	var output bytes.Buffer
	for _, path := range changedTrustFiles(current.Files, previous.Files) {
		old, next := before[path], after[path]
		if old.SHA256 == next.SHA256 {
			continue // Executable-bit changes are already in the summary.
		}
		var oldData, newData []byte
		var err error
		oldName, newName := "/dev/null", "/dev/null"
		if old.SHA256 != "" {
			oldName = "trusted/" + path
			oldData, err = readTrustBlob(env, old.SHA256)
			if err != nil {
				fmt.Fprintf(&output, "%s: %v; no diff\n", path, err)
				continue
			}
		}
		if next.SHA256 != "" {
			newName = "current/" + path
			newData, err = readTrustContents(filepath.Dir(project), next)
			if err != nil {
				fmt.Fprintf(&output, "%s: %v; no diff\n", path, err)
				continue
			}
		}
		if bytes.IndexByte(oldData[:min(len(oldData), 8192)], 0) >= 0 || bytes.IndexByte(newData[:min(len(newData), 8192)], 0) >= 0 {
			fmt.Fprintf(&output, "%s: binary files differ (old %.8s, new %.8s)\n", path, old.SHA256, next.SHA256)
			continue
		}
		if trustDiffLines(oldData) > 20000 || trustDiffLines(newData) > 20000 {
			fmt.Fprintf(&output, "%s: diff too large; files changed\n", path)
			continue
		}
		output.Write(textdiff.Diff(oldName, oldData, newName, newData))
	}
	return output.Bytes()
}

func trustDiffLines(data []byte) int {
	n := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

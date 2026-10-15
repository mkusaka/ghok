package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

func filesForHook(files []trustedFile, id string) []trustedFile {
	var result []trustedFile
	for _, file := range files {
		if slices.Contains(file.HookIDs, id) {
			result = append(result, file)
		}
	}
	return result
}

func selectedTrustFiles(files []trustedFile, selected map[string]bool) []trustedFile {
	var result []trustedFile
	for _, file := range files {
		var ids []string
		for _, id := range file.HookIDs {
			if selected[id] {
				ids = append(ids, id)
			}
		}
		if len(ids) != 0 {
			file.HookIDs = ids
			result = append(result, file)
		}
	}
	return result
}

// A selectively trusted shared file can have distinct trusted bytes for each
// hook. Keep both snapshots until all affected hooks have been re-trusted.
func mergeSelectedTrust(current, previous trustEntry, selected map[string]bool) trustEntry {
	result := current
	result.HookDigests = make(map[string]string)
	result.Definitions = make(map[string]json.RawMessage)
	for id, digest := range previous.HookDigests {
		if !selected[id] {
			result.HookDigests[id] = digest
			result.Definitions[id] = previous.Definitions[id]
		}
	}
	for id := range selected {
		result.HookDigests[id] = current.HookDigests[id]
		result.Definitions[id] = current.Definitions[id]
	}
	result.HookIDs = nil
	retained := make(map[string]bool)
	for id := range result.HookDigests {
		result.HookIDs = append(result.HookIDs, id)
		retained[id] = !selected[id]
	}
	sort.Strings(result.HookIDs)
	result.Files = selectedTrustFiles(current.Files, selected)
	for _, old := range selectedTrustFiles(previous.Files, retained) {
		found := false
		for i, file := range result.Files {
			if sameTrustedFile(file, old) {
				result.Files[i].HookIDs = append(file.HookIDs, old.HookIDs...)
				sort.Strings(result.Files[i].HookIDs)
				found = true
				break
			}
		}
		if !found {
			result.Files = append(result.Files, old)
		}
	}
	sort.Slice(result.Files, func(i, j int) bool {
		a, b := result.Files[i], result.Files[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.SHA256 < b.SHA256
	})
	return result
}

func sameTrustedFile(a, b trustedFile) bool {
	return a.Path == b.Path && a.SHA256 == b.SHA256 && a.Missing == b.Missing && a.Executable == b.Executable
}

type hookFileChange struct {
	before, after trustedFile
	ids           []string
}

func hookTrustReview(project string, current trustEntry, previous *trustEntry, selected map[string]bool, env map[string]string, noDiff bool) ([]string, []byte) {
	var notes []string
	if previous == nil {
		notes = append(notes, "no previous trust record for this repository")
	} else {
		notes = append(notes, fmt.Sprintf("Previous trust: trustedAt: %s; path: %s", previous.TrustedAt.Format(time.RFC3339), previous.FirstPath))
	}
	var changes []hookFileChange
	for _, id := range current.HookIDs {
		if !selected[id] || previous == nil {
			continue
		}
		before, after := filesForHook(previous.Files, id), filesForHook(current.Files, id)
		oldFiles, newFiles := make(map[string]trustedFile), make(map[string]trustedFile)
		for _, file := range before {
			oldFiles[file.Path] = file
		}
		for _, file := range after {
			newFiles[file.Path] = file
		}
		for _, path := range changedTrustFiles(after, before) {
			old, next := oldFiles[path], newFiles[path]
			found := false
			for i, change := range changes {
				if sameTrustedFile(change.before, old) && sameTrustedFile(change.after, next) {
					changes[i].ids = append(change.ids, id)
					found = true
					break
				}
			}
			if !found {
				changes = append(changes, hookFileChange{before: old, after: next, ids: []string{id}})
			}
		}
	}
	var diff bytes.Buffer
	render := func(change hookFileChange) {
		old, next := change.before, change.after
		path, kind := next.Path, "Changed"
		if old.Path == "" || old.Missing && !next.Missing {
			kind = "Added"
		}
		if next.Path == "" || !old.Missing && next.Missing {
			kind = "Removed"
			path = old.Path
		}
		notes = append(notes, kind+" files since trust: "+path)
		if old.Executable != next.Executable && old.Path != "" && next.Path != "" && !old.Missing && !next.Missing {
			mode := func(executable bool) string {
				if executable {
					return "0755"
				}
				return "0644"
			}
			notes = append(notes, fmt.Sprintf("%s: mode %s → %s", path, mode(old.Executable), mode(next.Executable)))
		}
		if !noDiff {
			before, after := trustEntry{}, trustEntry{}
			if old.Path != "" {
				before.Files = []trustedFile{old}
			}
			if next.Path != "" {
				after.Files = []trustedFile{next}
			}
			diff.Write(trustContentDiff(project, after, &before, env))
		}
	}
	for _, id := range current.HookIDs {
		if !selected[id] {
			continue
		}
		heading := "Hook " + id + ":"
		notes = append(notes, heading, "Trust digest: "+current.HookDigests[id])
		if previous == nil || !bytes.Equal(previous.Definitions[id], current.Definitions[id]) {
			notes = append(notes, "definition changed")
		}
		fmt.Fprintln(&diff, heading)
		for _, change := range changes {
			if len(change.ids) == 1 && change.ids[0] == id {
				render(change)
			}
		}
	}
	shared := false
	for _, change := range changes {
		if len(change.ids) <= 1 {
			continue
		}
		if !shared {
			notes = append(notes, "Shared files:")
			fmt.Fprintln(&diff, "Shared files:")
			shared = true
		}
		notes = append(notes, "hooks: "+strings.Join(change.ids, ", "))
		fmt.Fprintf(&diff, "hooks: %s\n", strings.Join(change.ids, ", "))
		render(change)
	}
	if noDiff || len(changes) == 0 {
		return notes, nil
	}
	return notes, diff.Bytes()
}

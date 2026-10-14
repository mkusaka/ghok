// audit-merge appends one JSON line per invocation containing selected merge
// operations and the real gh's exit status. An exit code of zero records a
// successful command, NOT proof that GitHub merged a PR (auto/queue may defer it).
// Build from the repository root:
//
//	go build -o ./examples/hooks/audit-merge.bin ./examples/hooks/audit-merge
//
// Installed as <config>/hooks/audit-merge, it writes to
// <config>/logs/merge-audit.jsonl by default. An optional argument overrides
// that path. The parent directory must already exist. See examples/config.yaml.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "audit-merge:", err)
		os.Exit(1) // Post-hook failures are diagnostic; they never change gh's exit.
	}
}

func run() error {
	if len(os.Args) > 2 {
		return fmt.Errorf("usage: audit-merge [audit-log-path]")
	}
	var path string
	if len(os.Args) == 2 {
		path = os.Args[1]
	} else {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		path = filepath.Join(filepath.Dir(executable), "..", "logs", "merge-audit.jsonl")
	}
	var input protocol.Input
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.SchemaVersion != 1 || input.Event != protocol.EventPost || input.Result == nil {
		return fmt.Errorf("expected schemaVersion 1 post event with result")
	}
	var merges []model.Operation
	for _, operation := range input.Operations {
		if strings.HasPrefix(operation.Action, "pr.merge.") && operation.Execution != model.ExecutionNotSelected && operation.Execution != model.ExecutionSkipped {
			merges = append(merges, operation)
		}
	}
	if len(merges) == 0 {
		return nil // Empty stdout means abstain.
	}
	// Input has already been redacted by ghok. Do not copy captured output:
	// this audit trail needs only the status and normalized merge operations.
	record := struct {
		Time       time.Time         `json:"time"`
		EventID    string            `json:"eventId"`
		Operations []model.Operation `json:"operations"`
		ExitCode   int               `json:"exitCode"`
		Signal     string            `json:"signal,omitempty"`
		StartError string            `json:"startError,omitempty"`
	}{time.Now().UTC(), input.EventID, merges, input.Result.ExitCode, input.Result.Signal, input.Result.StartError}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	// One append write keeps cooperating local-file writers' lines together.
	// This example does not provide rotation, fsync, or network-FS guarantees.
	line = append(line, '\n')
	n, err := file.Write(line)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if n != len(line) {
		return io.ErrShortWrite
	}
	return closeErr
}

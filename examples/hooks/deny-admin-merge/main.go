// deny-admin-merge rejects explicitly requested administrator merges.
// Build from the repository root:
//
//	go build -o ./examples/hooks/deny-admin-merge.bin ./examples/hooks/deny-admin-merge
//
// See examples/config.yaml for installation and the equivalent Python hook.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "deny-admin-merge:", err)
		os.Exit(1) // The configured pre-hook onError: deny fails closed.
	}
}

func run() error {
	var input protocol.Input
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.SchemaVersion != 1 || input.Event != protocol.EventPre {
		return fmt.Errorf("expected schemaVersion 1 pre event")
	}
	response := protocol.Response{SchemaVersion: 1, Decision: protocol.DecisionAbstain}
	for _, operation := range input.Operations {
		if operation.Execution == model.ExecutionNotSelected || operation.Execution == model.ExecutionSkipped {
			continue
		}
		if strings.HasPrefix(operation.Action, "pr.merge.") && operation.Parameters["admin"] == true {
			response.Decision = protocol.DecisionDeny
			response.Reason = "administrator merge bypass is disabled"
			response.Message = "Remove --admin and use the repository's normal merge checks."
			break
		}
	}
	return json.NewEncoder(os.Stdout).Encode(response)
}

// attribute-session appends the caller's Claude Code session ID to supplied bodies.
// Build from the repository root:
// go build -o "$config_dir/transforms/attribute-session" ./examples/transforms/attribute-session
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/mkusaka/ghok/internal/protocol"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "attribute-session:", err)
		os.Exit(1)
	}
}

func run() error {
	var input protocol.Input
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	session := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if session == "" {
		return nil
	}
	set := make(map[string]any)
	for i, op := range input.Operations {
		if op.Texts == nil || op.Texts.Body == nil {
			continue
		}
		body := op.Texts.Body
		if body.Truncated || body.Status != "" || (body.Source != "inline" && body.Source != "file" && body.Source != "stdin") {
			return fmt.Errorf("operation %d body is unavailable or generated", i)
		}
		set["/operations/"+strconv.Itoa(i)+"/texts/body/value"] = body.Value + "\n\nfrom session_id: " + session
	}
	if len(set) == 0 {
		return nil
	}
	return json.NewEncoder(os.Stdout).Encode(protocol.TransformResponse{SchemaVersion: 1, Set: set})
}

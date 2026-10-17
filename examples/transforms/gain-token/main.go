// gain-token swaps gh credentials for a freshly minted GitHub App token.
// Build from the repository root:
// go build -o "$config_dir/transforms/gain-token" ./examples/transforms/gain-token
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mkusaka/ghok/internal/protocol"
)

func main() {
	if err := run(); err != nil {
		// Do not include command output or errors that might contain credentials.
		fmt.Fprintln(os.Stderr, "gain-token: unable to mint or return token")
		os.Exit(1)
	}
}

func run() error {
	var input protocol.Input
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	args := []string{}
	if profile := os.Getenv("GAIN_PROFILE"); profile != "" {
		args = append(args, "--profile", profile)
	}
	args = append(args, "token")
	output, err := exec.Command("gain", args...).Output()
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(output))
	if token == "" {
		return fmt.Errorf("empty token")
	}
	return json.NewEncoder(os.Stdout).Encode(protocol.TransformResponse{
		SchemaVersion: 1,
		Env: map[string]*string{
			"GH_TOKEN":     &token,
			"GITHUB_TOKEN": nil,
		},
	})
}

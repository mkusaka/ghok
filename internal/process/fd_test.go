//go:build unix

package process

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

func TestReplayCaptureDescriptorsNotInherited(t *testing.T) {
	cfg := childConfig(t, "metadata", 0)
	direct := testCommand(t, cfg.Path)
	direct.Args = cfg.Args
	direct.Env = cfg.Env
	b, err := direct.Output()
	if err != nil {
		t.Fatal(err)
	}
	var baseline metadata
	if err = json.Unmarshal(b, &baseline); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"exec", "run-file", "run-pump"} {
		t.Run(mode, func(t *testing.T) {
			cfg := childConfig(t, "metadata", 0)
			cfg.Spool = true
			cfg.SpoolLimit = 1024
			if mode == "exec" {
				cfg.Mode = "exec"
			} else {
				cfg.Capture = true
			}
			if mode == "run-pump" {
				cfg.SpoolLimit = 1
			}
			cmd := wrapperCommand(t, cfg)
			cmd.Stdin = bytes.NewReader([]byte{0, 255, 1, 2, 3})
			b, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			var got metadata
			if err = json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got.FDs) != fmt.Sprint(baseline.FDs) {
				t.Fatalf("owned replay/capture fd inherited: got %v direct %v", got.FDInfo, baseline.FDInfo)
			}
		})
	}
}

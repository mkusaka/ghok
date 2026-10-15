//go:build unix

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustDryRunAndReviewHints(t *testing.T) {
	s := newSandbox(t)
	project, body := writeTrustProject(t, s)
	path := "review [x] ' data.txt"
	secret := "stored-trust-" + "private-value"
	s.env["GH_TOKEN"] = secret
	body = append(body, fmt.Appendf(nil, "trust: {include: [%q]}\n", path)...)
	if err := os.WriteFile(project, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.cwd, path), []byte("dirty trusted "+secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result := s.management(nil, "config", "trust", "--dry-run")
	result.code(t, 0)
	store := filepath.Join(filepath.Dir(s.cfg), "trust.json")
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote trust store: %v", err)
	}
	assertNoProjectHook(t, s)
	s.management(nil, "config", "trust", "--list", "--dry-run").code(t, 2)
	s.management(nil, "config", "trust", "--list", "--no-diff").code(t, 2)
	s.management(nil, "config", "untrust", "--dry-run").code(t, 2)
	s.management(nil, "config", "untrust", "--no-diff").code(t, 2)
	s.management(nil, "config", "trust").code(t, 0)
	before, err := os.ReadFile(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project, append(body, []byte("# inspect this change\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.cwd, path), []byte("new content "+secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(s.cwd, path), 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"config", "trust", "--dry-run"}, {"config", "validate"}, {"doctor", "--json"}} {
		setBytes(s.env, "FAKEGH_STDOUT_B64", []byte("gh version 2.102.0 (fake)\n"))
		result = s.management(nil, args...)
		result.code(t, 0)
		text := string(result.stdout)
		if args[0] == "doctor" {
			var checks []struct{ Detail string }
			decodeJSON(t, result.stdout, &checks)
			text = ""
			for _, check := range checks {
				text += check.Detail + "\n"
			}
		}
		for _, want := range []string{"Previous trust: trustedAt:", path, "mode 0644 → 0755"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%v omitted %q: %s", args, want, text)
			}
		}
		if args[1] == "trust" {
			// Credentials are masked in the displayed diff by default (agents keep
			// this output in their transcripts); the diff itself is still shown.
			for _, want := range []string{"-dirty trusted [REDACTED]", "+new content [REDACTED]"} {
				if !strings.Contains(text, want) {
					t.Fatalf("review must show the masked diff %q: %s", want, text)
				}
			}
			if strings.Contains(text, secret) {
				t.Fatalf("default trust diff exposed a credential: %s", text)
			}
		} else if strings.Contains(text, secret) || strings.Contains(text, "--- trusted/") {
			t.Fatalf("diagnostics must not expose content diffs: %s", text)
		}
	}
	result = s.management(nil, "config", "trust", "--dry-run", "--show-secrets")
	result.code(t, 0)
	for _, want := range []string{"UNREDACTED", "-dirty trusted " + secret, "+new content " + secret} {
		if !strings.Contains(string(result.stdout), want) {
			t.Fatalf("--show-secrets must show exact trusted/current bytes %q: %s", want, result.stdout)
		}
	}
	s.management(nil, "config", "trust", "--no-diff", "--show-secrets").code(t, 2)
	result = s.management(nil, "config", "trust", "--dry-run", "--no-diff")
	result.code(t, 0)
	if bytes.Contains(result.stdout, []byte(secret)) || bytes.Contains(result.stdout, []byte("--- trusted/")) || !bytes.Contains(result.stdout, []byte("Changed files since trust:")) {
		t.Fatalf("--no-diff must omit contents but keep review summaries: %s", result.stdout)
	}
	after, err := os.ReadFile(store)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("diagnostics changed trust: %v", err)
	}
	assertIgnoredProjectHook(t, s, "changed since trust")
	result = s.management(nil, "config", "trust")
	result.code(t, 0)
	if !bytes.Contains(result.stdout, []byte("-dirty trusted [REDACTED]")) || !bytes.Contains(result.stdout, []byte("+new content [REDACTED]")) || bytes.Contains(result.stdout, []byte(secret)) {
		t.Fatalf("trust must show the (masked) stored baseline before replacing it: %s", result.stdout)
	}
	s.management(nil, "config", "untrust", "--all").code(t, 0)
	blobs, err := os.ReadDir(filepath.Join(filepath.Dir(s.cfg), "trust", "blobs"))
	if err != nil || len(blobs) != 0 {
		t.Fatalf("untrust --all retained file copies: %v, %v", blobs, err)
	}
}

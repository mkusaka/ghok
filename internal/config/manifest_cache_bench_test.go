package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Both cases use the persistent-cache implementation. Cold removes its state
// before each load; warm measures reuse across independent LoadEffective calls.
func BenchmarkLoadEffectiveManifestCache(b *testing.B) {
	for _, state := range []string{"cold", "warm"} {
		b.Run(state, func(b *testing.B) {
			root := b.TempDir()
			repo := filepath.Join(root, "repo")
			env := map[string]string{"HOME": root, "GHOK_CONFIG_DIR": filepath.Join(root, "config")}
			if err := os.MkdirAll(repo, 0o700); err != nil {
				b.Fatal(err)
			}
			write := func(path, data string) {
				b.Helper()
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			write(filepath.Join(repo, ".git"), "gitdir: elsewhere\n")
			write(filepath.Join(repo, "hook"), strings.Repeat("h", 4096))
			var include []string
			for i := range 48 {
				name := fmt.Sprintf("data-%02d", i)
				include = append(include, name)
				write(filepath.Join(repo, name), strings.Repeat("d", 4096))
			}
			write(filepath.Join(repo, ".ghok.yaml"), "version: 1\nhooks: {pre: [{id: local, command: [./hook]}]}\ntrust: {include: ["+strings.Join(include, ",")+"]}\n")
			if _, _, _, _, err := TrustProject("", repo, env, false, false); err != nil {
				b.Fatal(err)
			}
			cache := filepath.Join(env["GHOK_CONFIG_DIR"], "state", "manifest-cache.json")
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if state == "cold" {
					if err := os.Remove(cache); err != nil && !os.IsNotExist(err) {
						b.Fatal(err)
					}
				}
				c, _, err := LoadEffective("", repo, env)
				if err != nil || len(c.Hooks) != 1 {
					b.Fatalf("load trusted fixture: %v, %v", c, err)
				}
			}
		})
	}
}

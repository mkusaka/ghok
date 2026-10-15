package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNestedTokenLifecycle(t *testing.T) {
	dir := t.TempDir()
	first, cleanupFirst, err := NewNestedToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupFirst()
	second, cleanupSecond, err := NewNestedToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupSecond()
	if first == second || len(first) != 64 || len(second) != 64 {
		t.Fatal("expected distinct 32-byte hex tokens")
	}
	env := map[string]string{"GHOK_NESTED": "1", "GHOK_NESTED_TOKEN": first}
	if !ValidNestedToken(dir, env) {
		t.Fatal("live token rejected")
	}
	if ValidNestedToken(t.TempDir(), env) {
		t.Fatal("token accepted in another directory")
	}
	for _, path := range []string{filepath.Join(dir, "state"), filepath.Join(dir, "state", "nested"), nestedTokenPath(dir, first)} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0700)
		if info.Mode().IsRegular() {
			want = 0600
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
		}
	}
	cleanupFirst()
	cleanupFirst()
	if ValidNestedToken(dir, env) {
		t.Fatal("cleaned token accepted")
	}
	if _, err := os.Lstat(nestedTokenPath(dir, first)); !os.IsNotExist(err) {
		t.Fatalf("cleaned token still exists: %v", err)
	}
	env["GHOK_NESTED_TOKEN"] = second
	if !ValidNestedToken(dir, env) {
		t.Fatal("cleanup removed another invocation's token")
	}
}

func TestNestedTokenRejectsForgedEnvironment(t *testing.T) {
	dir := t.TempDir()
	token, cleanup, err := NewNestedToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, env := range []map[string]string{
		nil,
		{"GHOK_NESTED": "1"},
		{"GHOK_NESTED_TOKEN": token},
		{"GHOK_NESTED": "0", "GHOK_NESTED_TOKEN": token},
		{"GHOK_NESTED": "1", "GHOK_NESTED_TOKEN": strings.Repeat("a", 64)},
	} {
		if ValidNestedToken(dir, env) {
			t.Fatal("forged or incomplete environment accepted")
		}
	}
	for _, invalid := range []string{"", "../token", strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("g", 64), strings.Repeat("A", 64)} {
		// Even an existing hash-named file cannot authorize malformed input.
		if err := os.WriteFile(nestedTokenPath(dir, invalid), nil, 0600); err != nil {
			t.Fatal(err)
		}
		if ValidNestedToken(dir, map[string]string{"GHOK_NESTED": "1", "GHOK_NESTED_TOKEN": invalid}) {
			t.Fatal("invalid token shape accepted")
		}
	}
}

func TestNestedTokenRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"public", "symlink", "directory", "missing"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			token, cleanup, err := NewNestedToken(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			path := nestedTokenPath(dir, token)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "public":
				err = os.WriteFile(path, nil, 0600)
				if err == nil {
					err = os.Chmod(path, 0644)
				}
			case "symlink":
				target := filepath.Join(dir, "target")
				if err = os.WriteFile(target, nil, 0600); err == nil {
					err = os.Symlink(target, path)
				}
			case "directory":
				err = os.Mkdir(path, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if ValidNestedToken(dir, map[string]string{"GHOK_NESTED": "1", "GHOK_NESTED_TOKEN": token}) {
				t.Fatal("unsafe token file accepted")
			}
		})
	}
}

func TestNestedTokenRejectsUnsafeDirectories(t *testing.T) {
	for _, component := range []string{"state", "nested"} {
		for _, kind := range []string{"public", "symlink", "file"} {
			t.Run(component+"/"+kind, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "state")
				if component == "nested" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
					path = filepath.Join(path, "nested")
				}
				var err error
				switch kind {
				case "public":
					err = os.Mkdir(path, 0700)
					if err == nil {
						err = os.Chmod(path, 0755)
					}
				case "symlink":
					err = os.Symlink(t.TempDir(), path)
				case "file":
					err = os.WriteFile(path, nil, 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, cleanup, err := NewNestedToken(dir); err == nil {
					cleanup()
					t.Fatal("unsafe directory accepted")
				}
				if ValidNestedToken(dir, map[string]string{"GHOK_NESTED": "1", "GHOK_NESTED_TOKEN": strings.Repeat("a", 64)}) {
					t.Fatal("unsafe directory validated")
				}
			})
		}
	}
}

//go:build unix

package bench

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/config"
	"github.com/mkusaka/ghok/internal/hooks"
	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/normalize"
	"github.com/mkusaka/ghok/internal/protocol"
)

var (
	benchmarkDir string
	shimPath     string
	fakePath     string
	benchmarkEnv []string
	resultSink   model.Result
)

func TestMain(m *testing.M) {
	flag.Parse()
	if flag.Lookup("test.bench").Value.String() == "" {
		os.Exit(m.Run()) // Ordinary unit-test runs need no subprocess fixtures.
	}
	code := runBenchmarks(m)
	os.Exit(code)
}

func runBenchmarks(m *testing.M) int {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	benchmarkDir, err = os.MkdirTemp("", "ghok-bench-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(benchmarkDir)
	for _, name := range []string{"bin", "real", "home", "config", "gh-config", "tmp", "work"} {
		if err := os.MkdirAll(filepath.Join(benchmarkDir, name), 0700); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	ghokPath := filepath.Join(benchmarkDir, "bin", "ghok")
	shimPath = filepath.Join(benchmarkDir, "bin", "gh")
	fakePath = filepath.Join(benchmarkDir, "real", "gh")
	// Compilation alone uses the normal Go module/build caches. Disable module
	// downloads and toolchain switching; measured children get only the explicit
	// sandbox environment below, never the caller's credentials/config/PATH.
	for _, target := range []struct{ output, source string }{
		{ghokPath, "./cmd/ghok"},
		{fakePath, "./test/fakegh"},
	} {
		cmd := exec.Command("go", "build", "-o", target.output, target.source)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOWORK=off")
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", target.source, err, output)
			return 1
		}
	}
	if err := os.Symlink(ghokPath, shimPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	benchmarkEnv = []string{
		"HOME=" + filepath.Join(benchmarkDir, "home"),
		"XDG_CONFIG_HOME=" + filepath.Join(benchmarkDir, "config"),
		"GHOK_CONFIG_DIR=" + filepath.Join(benchmarkDir, "config", "ghok"),
		"GH_CONFIG_DIR=" + filepath.Join(benchmarkDir, "gh-config"),
		"TMPDIR=" + filepath.Join(benchmarkDir, "tmp"),
		"PATH=" + filepath.Dir(shimPath) + string(os.PathListSeparator) + filepath.Dir(fakePath),
		"GHOK_REAL_GH=" + fakePath,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"LANG=C",
		"TZ=UTC",
	}
	return m.Run()
}

func BenchmarkStartup(b *testing.B) {
	workDir := filepath.Join(benchmarkDir, "work")
	for _, target := range []struct{ name, path string }{
		{"DirectFakeGH", fakePath},
		{"ShimNoConfig", shimPath},
	} {
		b.Run(target.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				cmd := exec.Command(target.path, "pr", "view", "123", "-R", "org/repo")
				cmd.Env = benchmarkEnv
				cmd.Dir = workDir
				if err := cmd.Run(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkNormalize(b *testing.B) {
	cases := []struct {
		name  string
		level model.Level
		argv  []string
	}{
		{"L1NativeMerge", model.LevelL1, []string{"pr", "merge", "123", "--squash", "--admin", "-R", "org/repo"}},
		{"L2RESTMerge", model.LevelL2, []string{"api", "-X", "PUT", "repos/org/repo/pulls/123/merge", "-f", "merge_method=squash"}},
		{"L3GraphQLMerge", model.LevelL3, []string{"api", "graphql", "-f", `query=mutation Merge($id: ID!) { mergePullRequest(input: {pullRequestId: $id, mergeMethod: SQUASH}) { pullRequest { number } } }`, "-f", "id=PR_benchmark"}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			invocation := model.Invocation{Argv: tc.argv, Env: map[string]string{}, Cwd: filepath.Join(benchmarkDir, "work")}
			result := normalize.Normalize(invocation)
			if result.Classification.Level != tc.level || len(result.Operations) != 1 || result.Operations[0].Family != "pr.merge" {
				b.Fatalf("fixture did not normalize as a %s merge: %+v", tc.level, result)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				resultSink = normalize.Normalize(invocation)
			}
		})
	}
}

func BenchmarkTrivialHook(b *testing.B) {
	hook := config.Hook{
		ID: "trivial", Phase: config.PhasePre,
		Command: []string{fakePath}, Timeout: config.Duration(5 * time.Second), OnError: config.OnErrorDeny,
	}
	runner := hooks.Runner{
		Env:     append(append([]string(nil), benchmarkEnv...), "FAKEGH_MODE=hook"),
		BaseDir: benchmarkDir,
		RealGh:  fakePath,
	}
	selected := []config.Hook{hook}
	input := protocol.Input{
		SchemaVersion: 1, Event: protocol.EventPre, EventID: "benchmark",
		Invocation: protocol.InvocationInfo{Argv: []string{"pr", "merge", "123"}, Cwd: filepath.Join(benchmarkDir, "work")},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		result := runner.Run(context.Background(), config.PhasePre, selected, input)
		if len(result.Results) != 1 || result.Results[0].Outcome != protocol.OutcomeOK || result.Decision != protocol.DecisionAbstain {
			b.Fatalf("trivial hook failed: %+v", result)
		}
	}
}

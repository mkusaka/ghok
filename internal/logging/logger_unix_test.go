//go:build unix

package logging

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mkusaka/ghok/internal/model"
	"github.com/mkusaka/ghok/internal/protocol"
	"github.com/mkusaka/ghok/internal/redact"
)

func testLogger(t testing.TB, path, level string) *Logger {
	t.Helper()
	logger, err := OpenWithRedactor(path, level, redact.New(map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logger.Close(); err != nil {
			t.Error(err)
		}
	})
	return logger
}

func readRecords(t testing.TB, paths ...string) []Record {
	t.Helper()
	var records []Record
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != 0 && data[len(data)-1] != '\n' {
			t.Fatalf("%s: unterminated record", path)
		}
		lines := bytes.Split(data, []byte{'\n'})
		for _, line := range lines[:len(lines)-1] {
			var record Record
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatalf("%s: invalid JSON record: %v", path, err)
			}
			records = append(records, record)
		}
	}
	return records
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s: mode %04o, want %04o", path, got, want)
	}
}

func TestOpenPermissionsAndAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "nested", "ghok.jsonl")
	logger := testLogger(t, path, "info")
	assertMode(t, filepath.Dir(path), 0700)
	assertMode(t, filepath.Dir(filepath.Dir(path)), 0700)
	assertMode(t, path, 0600)
	assertMode(t, path+".lock", 0600)
	if err := logger.Write(Record{InvocationID: "first", Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{path, path + ".lock"} {
		if err := os.Chmod(file, 0644); err != nil {
			t.Fatal(err)
		}
	}
	logger = testLogger(t, path, "")
	if err := logger.Write(Record{InvocationID: "second", Decision: "audit"}); err != nil {
		t.Fatal(err)
	}
	// A pre-existing directory is never chmod'ed (it may be shared).
	assertMode(t, filepath.Dir(path), 0755)
	assertMode(t, path, 0600)
	assertMode(t, path+".lock", 0600)
	records := readRecords(t, path)
	if len(records) != 2 || records[0].InvocationID != "first" || records[1].InvocationID != "second" {
		t.Fatalf("unexpected appended records: %+v", records)
	}
	if records[0].Time.IsZero() || records[0].Time.Location() != time.UTC {
		t.Errorf("missing UTC timestamp: %v", records[0].Time)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(bytes.Split(data, []byte{'\n'})[0], &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"time", "invocationId", "mode", "durationMs", "argv", "classification", "operations", "decision"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing lowerCamelCase field %q", key)
		}
	}
}

func TestOpenRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"log-symlink", "lock-symlink", "directory-symlink", "log-directory", "log-fifo"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "logs", "ghok.jsonl")
			if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "target")
			if err := os.WriteFile(target, []byte("unchanged"), 0644); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "log-symlink":
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "lock-symlink":
				if err := os.Symlink(target, path+".lock"); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink":
				path = filepath.Join(root, "linked", "ghok.jsonl")
				if err := os.Symlink(filepath.Join(root, "logs"), filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			case "log-directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "log-fifo":
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			}
			logger, err := OpenWithRedactor(path, "info", redact.New(nil))
			if err == nil {
				_ = logger.Close()
				t.Fatal("unsafe path accepted")
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "unchanged" {
				t.Fatalf("symlink target changed: %q, %v", data, err)
			}
			assertMode(t, target, 0644)
		})
	}
}

func TestOpenValidationAndClose(t *testing.T) {
	for _, input := range []struct{ path, level string }{{"", "info"}, {filepath.Join(t.TempDir(), "log"), "debug"}} {
		if logger, err := Open(input.path, input.level); err == nil {
			_ = logger.Close()
			t.Errorf("accepted invalid input %+v", input)
		}
	}
	logger := testLogger(t, filepath.Join(t.TempDir(), "log"), "info")
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	if err := logger.Write(Record{}); !errors.Is(err, os.ErrClosed) {
		t.Errorf("write after close: %v", err)
	}
	var zero Logger
	if err := zero.Write(Record{}); !errors.Is(err, os.ErrClosed) {
		t.Errorf("zero logger write: %v", err)
	}
}

func TestDecisionFiltering(t *testing.T) {
	cases := []struct {
		name   string
		record Record
		write  bool
	}{
		{"allow", Record{Decision: "allow"}, false},
		{"passthrough", Record{Decision: "passthrough"}, false},
		{"audit", Record{Decision: "audit"}, true},
		{"deny", Record{Decision: "deny"}, true},
		{"unknown", Record{}, true},
		{"errors", Record{Decision: "allow", Errors: []string{"error"}}, true},
		{"hook-error", Record{Decision: "allow", Hooks: []protocol.HookResult{{Outcome: protocol.OutcomeError}}}, true},
		{"hook-timeout", Record{Decision: "allow", Hooks: []protocol.HookResult{{Outcome: protocol.OutcomeTimeout}}}, true},
		{"hook-canceled", Record{Decision: "allow", Hooks: []protocol.HookResult{{Outcome: protocol.OutcomeCanceled}}}, true},
		{"hook-invalid", Record{Decision: "allow", Hooks: []protocol.HookResult{{Outcome: protocol.OutcomeInvalidResponse}}}, true},
		{"hook-explicit-error", Record{Decision: "allow", Hooks: []protocol.HookResult{{Error: "failure"}}}, true},
		{"hook-exit", Record{Decision: "allow", Hooks: []protocol.HookResult{{ExitCode: 1}}}, true},
		{"hook-ok", Record{Decision: "allow", Hooks: []protocol.HookResult{{Outcome: protocol.OutcomeOK}}}, false},
		{"hook-skipped", Record{Decision: "allow", Hooks: []protocol.HookResult{{Outcome: protocol.OutcomeSkipped}}}, false},
		{"start-error", Record{Decision: "allow", Exec: &protocol.ExecResult{StartError: "failed"}}, true},
		{"child-exit", Record{Decision: "allow", Exec: &protocol.ExecResult{ExitCode: 42}}, false},
		{"diagnostic", Record{Decision: "allow", Diagnostics: []string{"note"}}, false},
	}
	for _, level := range []string{"info", "decision"} {
		t.Run(level, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "log")
			logger := testLogger(t, path, level)
			var want []string
			for _, tc := range cases {
				tc.record.InvocationID = tc.name
				if err := logger.Write(tc.record); err != nil {
					t.Fatal(err)
				}
				if level == "info" || tc.write {
					want = append(want, tc.name)
				}
			}
			var got []string
			for _, record := range readRecords(t, path) {
				got = append(got, record.InvocationID)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("records %v, want %v", got, want)
			}
		})
	}
}

func TestRedactionDoesNotMutateRecord(t *testing.T) {
	secret := "fake-learned-" + "secret-123456789"
	body := "captured output must never be logged"
	path := filepath.Join(t.TempDir(), "log")
	logger, err := OpenWithRedactor(path, "info", redact.New(map[string]string{"GH_TOKEN": secret}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := logger.Close(); err != nil {
			t.Error(err)
		}
	}()
	record := Record{
		Time:         time.Date(2026, 10, 14, 1, 2, 3, 4, time.UTC),
		InvocationID: secret, Mode: secret, DurationMs: 123,
		Argv:  []string{"api", "--token", "short-value", "-H", "Authorization: Bearer short-value", secret},
		Alias: secret, Decision: secret,
		Classification: model.Classification{Level: model.Level(secret), Confidence: model.Confidence(secret), Resolution: model.Resolution(secret)},
		Operations: []model.Operation{{
			Family: secret, Action: secret, Effect: model.Effect(secret), Execution: model.Execution(secret),
			UnknownReason: model.UnknownReason(secret), Evidence: secret, Detail: secret,
			Target:     model.Target{Host: secret, Owner: secret, Repository: secret, NodeID: secret, Branch: secret, BaseBranch: secret, HeadBranch: secret, SHA: secret, Ref: secret, URL: "https://user:password@example.com/"},
			Parameters: map[string]any{"password": "short-value", "nested": []any{map[string]any{"token": "short-value", "normal": secret}}},
			Unresolved: []model.Unresolved{{Field: secret, Reason: secret, Source: secret}},
			Origin:     model.Origin{Command: []string{secret}, Method: secret, Route: secret, Field: secret, Alias: secret, Operation: secret, OpType: secret},
		}},
		Errors: []string{secret}, Diagnostics: []string{secret},
		Policy: &protocol.PolicyInfo{Mode: secret, Decision: protocol.Decision(secret), Reasons: []string{secret}, Rules: []string{secret}},
		Hooks:  []protocol.HookResult{{HookID: secret, Phase: secret, Outcome: protocol.Outcome(secret), Decision: protocol.Decision(secret), Reason: secret, Message: secret, Effective: protocol.Decision(secret), Error: secret, Stderr: secret}},
		Exec:   &protocol.ExecResult{ExitCode: 42, Signal: secret, StartError: secret, Stdout: &protocol.Output{Base64: base64.StdEncoding.EncodeToString([]byte(body)), Size: int64(len(body))}, Stderr: &protocol.Output{Base64: body}},
	}
	before, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Write(record); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("Write mutated the caller's record")
	}
	if err := logger.Write(Record{Argv: []string{"secret", "set", "name", "--body", "short-body-secret"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret, "short-value", "short-body-secret", body, base64.StdEncoding.EncodeToString([]byte(body)), "password@example.com", `"stdout"`, `"stderr":{"base64"`} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Errorf("log contains unredacted or captured content %q", forbidden)
		}
	}
	logged := readRecords(t, path)[0]
	if logged.Exec == nil || logged.Exec.ExitCode != 42 || logged.Exec.Stdout != nil || logged.Exec.Stderr != nil {
		t.Errorf("incorrect exec metadata: %+v", logged.Exec)
	}
	if !logged.Time.Equal(record.Time) || logged.DurationMs != record.DurationMs {
		t.Error("timestamp or duration changed")
	}
}

func TestOpenLearnsProcessEnvironment(t *testing.T) {
	secret := "fake-environment-" + "token-987654321"
	t.Setenv("GH_TOKEN", secret)
	for _, injectedNil := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "log")
		var logger *Logger
		var err error
		if injectedNil {
			logger, err = OpenWithRedactor(path, "info", nil)
		} else {
			logger, err = Open(path, "info")
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := logger.Write(Record{Diagnostics: []string{secret}}); err != nil {
			t.Fatal(err)
		}
		if err := logger.Close(); err != nil {
			t.Fatal(err)
		}
		if got := readRecords(t, path)[0].Diagnostics[0]; got != "[REDACTED]" {
			t.Errorf("environment secret logged: %q", got)
		}
	}
}

func TestConcurrentGoroutines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	loggers := []*Logger{testLogger(t, path, "info"), testLogger(t, path, "info")}
	const workers, count = 16, 40
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for worker := range workers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range count {
				id := fmt.Sprintf("%d-%d", worker, i)
				if err := loggers[worker%len(loggers)].Write(Record{InvocationID: id, Decision: "allow", Diagnostics: []string{"embedded\nnewline\x00bytes"}}); err != nil {
					errs <- err
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	assertUniqueRecords(t, readRecords(t, path), workers*count)
}

func assertUniqueRecords(t *testing.T, records []Record, count int) {
	t.Helper()
	if len(records) != count {
		t.Fatalf("got %d records, want %d", len(records), count)
	}
	seen := make(map[string]bool, count)
	for _, record := range records {
		if seen[record.InvocationID] {
			t.Errorf("duplicate record %q", record.InvocationID)
		}
		seen[record.InvocationID] = true
	}
}

func seedOversize(t *testing.T, path, id string) {
	t.Helper()
	data, err := json.Marshal(Record{InvocationID: id, Diagnostics: []string{strings.Repeat("x", int(maxLogBytes))}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRotationRetainsThreeArchives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	logger := testLogger(t, path, "info")
	lockInfo, err := os.Stat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		seedOversize(t, path, "seed-"+strconv.Itoa(i))
		if err := logger.Write(Record{InvocationID: "current-" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
		assertMode(t, path, 0600)
	}
	for i := 1; i <= 3; i++ {
		archive := path + "." + strconv.Itoa(i)
		assertMode(t, archive, 0600)
		records := readRecords(t, archive)
		if len(records) != 1 || records[0].InvocationID != "seed-"+strconv.Itoa(6-i) {
			t.Errorf("incorrect archive %d", i)
		}
	}
	if _, err := os.Stat(path + ".4"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unexpected fourth archive: %v", err)
	}
	if got := readRecords(t, path); len(got) != 1 || got[0].InvocationID != "current-5" {
		t.Errorf("incorrect current file: %+v", got)
	}
	after, err := os.Stat(path + ".lock")
	if err != nil || !os.SameFile(lockInfo, after) {
		t.Fatal("rotation replaced stable lock file")
	}
}

func TestOversizedRecordRotatesOnNextWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	logger := testLogger(t, path, "info")
	large := Record{InvocationID: "large", Diagnostics: []string{strings.Repeat("x", int(maxLogBytes))}}
	if err := logger.Write(large); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rotated before next write: %v", err)
	}
	if err := logger.Write(Record{InvocationID: "next"}); err != nil {
		t.Fatal(err)
	}
	if records := readRecords(t, path+".1", path); len(records) != 2 || records[0].InvocationID != "large" || records[1].InvocationID != "next" {
		t.Fatal("rotation split or lost a record")
	}
}

func TestConcurrentProcesses(t *testing.T) {
	for _, rotation := range []bool{false, true} {
		t.Run(fmt.Sprintf("rotation-%t", rotation), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "log")
			logger := testLogger(t, path, "info")
			if rotation {
				seedOversize(t, path, "seed")
			}
			const workers, count = 4, 40
			runProcessWriters(t, path, workers, count)
			// The parent also opened before rotation. Its next write must go to
			// the current path rather than the descriptor of the old archive.
			if err := logger.Write(Record{InvocationID: "parent"}); err != nil {
				t.Fatal(err)
			}
			assertUniqueRecords(t, readRecords(t, path), workers*count+1)
			if rotation {
				records := readRecords(t, path+".1")
				if len(records) != 1 || records[0].InvocationID != "seed" {
					t.Fatal("a writer appended to the rotated file")
				}
			}
		})
	}
}

func runProcessWriters(t *testing.T, path string, workers, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	gateRead, gateWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gateRead.Close() }()
	defer func() { _ = gateWrite.Close() }()
	commands := make([]*exec.Cmd, 0, workers)
	outputs := make([]*bytes.Buffer, 0, workers)
	for worker := range workers {
		readyRead, readyWrite, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLoggingProcessHelper$")
		cmd.Env = []string{
			"HOME=" + t.TempDir(), "XDG_CONFIG_HOME=" + t.TempDir(), "PATH=" + t.TempDir(),
			"GHOK_LOGGING_HELPER=1", "GHOK_TEST_LOG=" + path,
			"GHOK_TEST_WORKER=" + strconv.Itoa(worker), "GHOK_TEST_COUNT=" + strconv.Itoa(count),
		}
		cmd.Stdin = gateRead
		cmd.ExtraFiles = []*os.File{readyWrite}
		output := new(bytes.Buffer)
		cmd.Stdout, cmd.Stderr = output, output
		if err := cmd.Start(); err != nil {
			_ = readyRead.Close()
			_ = readyWrite.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		if err := readyWrite.Close(); err != nil {
			_ = readyRead.Close()
			t.Fatal(err)
		}
		if err := readyRead.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			_ = readyRead.Close()
			t.Fatal(err)
		}
		var ready [1]byte
		_, err = io.ReadFull(readyRead, ready[:])
		_ = readyRead.Close()
		if err != nil {
			t.Fatalf("helper did not become ready: %v", err)
		}
		commands = append(commands, cmd)
		outputs = append(outputs, output)
	}
	if _, err := gateWrite.Write(bytes.Repeat([]byte{'x'}, workers)); err != nil {
		t.Fatal(err)
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Errorf("helper %d: %v: %s", i, err, outputs[i].String())
		}
	}
}

func TestLoggingProcessHelper(t *testing.T) {
	if os.Getenv("GHOK_LOGGING_HELPER") != "1" {
		return
	}
	logger, err := OpenWithRedactor(os.Getenv("GHOK_TEST_LOG"), "info", redact.New(map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	ready := os.NewFile(3, "ready")
	if _, err := ready.Write([]byte{'x'}); err != nil {
		t.Fatal(err)
	}
	if err := ready.Close(); err != nil {
		t.Fatal(err)
	}
	var gate [1]byte
	if _, err := io.ReadFull(os.Stdin, gate[:]); err != nil {
		t.Fatal(err)
	}
	count, err := strconv.Atoi(os.Getenv("GHOK_TEST_COUNT"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range count {
		id := os.Getenv("GHOK_TEST_WORKER") + "-" + strconv.Itoa(i)
		if err := logger.Write(Record{InvocationID: id, Decision: "allow", Diagnostics: []string{"line\nline\x00"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNewInvocationID(t *testing.T) {
	const count = 512
	ids := make(chan string, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids <- NewInvocationID()
		}()
	}
	wg.Wait()
	close(ids)
	seen := make(map[string]bool, count)
	for id := range ids {
		data, err := hex.DecodeString(id)
		if err != nil || len(data) != 16 || id != strings.ToLower(id) {
			t.Errorf("invalid invocation ID %q", id)
		}
		if seen[id] {
			t.Errorf("duplicate invocation ID %q", id)
		}
		seen[id] = true
	}
}

func TestConcurrentClose(t *testing.T) {
	logger := testLogger(t, filepath.Join(t.TempDir(), "log"), "info")
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				if err := logger.Write(Record{Decision: "allow"}); err != nil && !errors.Is(err, os.ErrClosed) {
					t.Error(err)
				}
			}
			if err := logger.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func BenchmarkLoggerWrite(b *testing.B) {
	path := filepath.Join(b.TempDir(), "logs", "ghok.jsonl")
	logger := testLogger(b, path, "info")
	record := Record{
		Time:         time.Date(2026, 10, 14, 1, 2, 3, 0, time.UTC),
		InvocationID: "0123456789abcdef0123456789abcdef", Mode: "wrapper",
		Argv: []string{"pr", "merge", "123", "--squash"}, Decision: "allow",
		Operations: []model.Operation{{Family: "pr.merge", Action: "pr.merge.request", Effect: model.EffectWrite, Target: model.Target{Repository: "org/repo"}, Parameters: map[string]any{"method": "squash"}}},
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := logger.Write(record); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoggerWriteFiltered(b *testing.B) {
	logger := testLogger(b, filepath.Join(b.TempDir(), "log"), "decision")
	record := Record{Decision: "allow", Argv: []string{"pr", "view", "123"}}
	b.ReportAllocs()
	for b.Loop() {
		if err := logger.Write(record); err != nil {
			b.Fatal(err)
		}
	}
}

func TestEncodingFailureDoesNotAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	logger := testLogger(t, path, "info")
	if err := logger.Write(Record{Time: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}); err == nil {
		t.Fatal("unrepresentable JSON timestamp accepted")
	}
	if err := logger.Write(Record{InvocationID: "valid"}); err != nil {
		t.Fatal(err)
	}
	records := readRecords(t, path)
	if len(records) != 1 || records[0].InvocationID != "valid" {
		t.Fatal("encoding failure left partial data")
	}
}

func TestWriteWithholdsOrdinaryContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	logger := testLogger(t, path, "info")
	prose := "ordinary private prose"
	document := `mutation { addComment(input: {subjectId: "ISSUE_12", body: "tiny"}) { id } }`
	argv := []string{"api", "graphql", "-fquery=" + document, "-finput[body]=tiny"}
	record := Record{
		Argv:   argv,
		Source: &model.Source{Kind: "graphql", GraphQL: document, Argv: argv},
		Operations: []model.Operation{{
			Family: "pr.comment", Action: "pr.comment.create",
			Target:     model.Target{Repository: "owner/repository", NodeID: "ISSUE_12"},
			Parameters: map[string]any{"body": "tiny", "nested": map[string]any{"unknown": prose}},
		}},
	}
	if err := logger.Write(record); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{prose, "tiny", "addComment", document} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Errorf("JSONL contains private content %q", forbidden)
		}
	}
	logged := readRecords(t, path)[0]
	if logged.Operations[0].Target.Repository != "owner/repository" ||
		logged.Operations[0].Target.NodeID != "ISSUE_12" ||
		logged.Operations[0].Action != "pr.comment.create" ||
		logged.Source.GraphQL != graphQLSummary(document) {
		t.Errorf("incorrect log metadata: %+v", logged)
	}
	if record.Operations[0].Parameters["body"] != "tiny" || record.Source.GraphQL != document {
		t.Fatal("Write changed hook-visible input")
	}
	if err := logger.Write(Record{Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	if logged := readRecords(t, path)[1]; logged.Source != nil {
		t.Fatal("absent source did not remain optional")
	}
}

func TestWarnedAllowRetentionAndRedactedCopy(t *testing.T) {
	secret := "warning-" + "credential"
	record := Record{
		Decision: "allow",
		Warnings: []protocol.Warning{{Source: secret, ID: secret, Text: secret}},
		Hooks:    []protocol.HookResult{{Warning: secret, Outcome: protocol.OutcomeOK}},
	}
	before, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "warnings.jsonl")
	logger, err := OpenWithRedactor(path, "decision", redact.New(map[string]string{"GH_TOKEN": secret}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.Close() }()
	if err := logger.Write(Record{Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	if err := logger.Write(record); err != nil {
		t.Fatal(err)
	}
	records := readRecords(t, path)
	if len(records) != 1 || records[0].Decision != "allow" || len(records[0].Warnings) != 1 || records[0].Warnings[0] != (protocol.Warning{Source: "[REDACTED]", ID: "[REDACTED]", Text: "[REDACTED]"}) || records[0].Hooks[0].Warning != "[REDACTED]" {
		t.Fatalf("warned allow not retained/redacted: %+v", records)
	}
	after, err := json.Marshal(record)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("logging mutated warning input: %s -> %s (%v)", before, after, err)
	}
}

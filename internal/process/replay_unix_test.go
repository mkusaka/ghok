//go:build unix

package process

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestInputReplayPartialStream(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close(); _ = write.Close() }()
	data := bytes.Repeat([]byte("binary\x00stream\xff\n"), 50000)
	done := make(chan error, 1)
	go func() { _, err := write.Write(data); _ = write.Close(); done <- err }()
	replay, err := NewInputReplay(read)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	attempt := replay.Attempt()
	if _, err := attempt.startPump(); err != nil {
		t.Fatal(err)
	}
	prefix := make([]byte, 5)
	if _, err := io.ReadFull(attempt.File(), prefix); err != nil {
		t.Fatal(err)
	}
	if err := attempt.Close(); err != nil {
		t.Fatal(err)
	}
	fallback, err := replay.Fallback()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fallback.Close() }()
	if _, err := fallback.startPump(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(fallback.File())
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("replay length=%d want=%d err=%v", len(got), len(data), err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestInputReplayRegularFileOffset(t *testing.T) {
	f, err := privateReplayFile()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString("skipremaining"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(4, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	replay, err := NewInputReplay(f)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	buf := make([]byte, 3)
	if _, err := replay.Attempt().File().Read(buf); err != nil {
		t.Fatal(err)
	}
	if err := replay.Attempt().Close(); err != nil {
		t.Fatal(err)
	}
	fallback, err := replay.Fallback()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fallback.Close() }()
	got, err := io.ReadAll(fallback.File())
	if err != nil || string(got) != "remaining" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

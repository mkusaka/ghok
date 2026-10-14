//go:build unix

package process

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"
)

func BenchmarkSpoolStdin(b *testing.B) {
	payload := bytes.Repeat([]byte{0, 'x', 255, '\n'}, 256*1024)
	b.Run("regular", func(b *testing.B) {
		f, err := os.CreateTemp(b.TempDir(), "input")
		if err != nil {
			b.Fatal(err)
		}
		defer func() {
			if err := f.Close(); err != nil {
				b.Error(err)
			}
		}()
		if _, err := f.Write(payload); err != nil {
			b.Fatal(err)
		}
		b.SetBytes(int64(len(payload)))
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				b.Fatal(err)
			}
			s, err := SpoolStdin(f, int64(len(payload)), 0)
			if err != nil {
				b.Fatal(err)
			}
			if err := s.Close(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("pipe", func(b *testing.B) {
		b.SetBytes(int64(len(payload)))
		b.ReportAllocs()
		for b.Loop() {
			r, w, err := os.Pipe()
			if err != nil {
				b.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := w.Write(payload)
				_ = w.Close()
				done <- err
			}()
			s, err := SpoolStdin(r, int64(len(payload)), commandTimeout)
			if err != nil {
				_ = r.Close()
				b.Fatal(err)
			}
			if err := s.Close(); err != nil {
				_ = r.Close()
				b.Fatal(err)
			}
			_ = r.Close()
			select {
			case err := <-done:
				if err != nil {
					b.Fatal(err)
				}
			case <-time.After(commandReapTimeout):
				_ = w.Close()
				b.Fatal("spool producer did not terminate")
			}
		}
	})
}

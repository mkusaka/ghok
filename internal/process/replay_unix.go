//go:build unix

package process

import (
	"fmt"
	"io"
	"os"
)

// InputReplay records a delegate's streamed stdin without pre-reading it. Bytes
// prefetched into its pipe are retained too. Close after the execution/fallback.
// Terminals remain inherited: a declining delegate must not consume TTY input.
// Call Fallback only after Run has stopped/joined the attempt's pump.
type InputReplay struct {
	source  *os.File
	record  *os.File
	attempt *Spool
	offset  int64
	seek    bool
	tty     bool
}

func NewInputReplay(source *os.File) (*InputReplay, error) {
	r := &InputReplay{source: source}
	info, err := source.Stat()
	if err != nil {
		return nil, err
	}
	if isTerminal(source) {
		r.tty = true
		return r, nil
	}
	if info.Mode().IsRegular() {
		r.offset, err = source.Seek(0, io.SeekCurrent)
		r.seek = true
		if err != nil {
			return nil, err
		}
		r.attempt = &Spool{source: source, replay: source}
		return r, nil
	}
	r.record, err = privateReplayFile()
	if err != nil {
		return nil, err
	}
	// A separate empty prefix lets the ordinary live replay pump record its
	// source reads without reading from the file it is appending to.
	prefix, err := privateReplayFile()
	if err == nil {
		r.attempt, err = liveReplay(source, prefix)
	}
	if err != nil {
		if prefix != nil {
			_ = prefix.Close()
		}
		r.Close()
		return nil, err
	}
	r.attempt.record = r.record
	return r, nil
}

func (r *InputReplay) Attempt() *Spool { return r.attempt }

func (r *InputReplay) Fallback() (*Spool, error) {
	if r.tty {
		return nil, nil
	}
	if r.seek {
		_, err := r.source.Seek(r.offset, io.SeekStart)
		return &Spool{source: r.source, replay: r.source}, err
	}
	if r.attempt.recordErr != nil {
		return nil, fmt.Errorf("cannot replay delegate stdin: %w", r.attempt.recordErr)
	}
	if _, err := r.record.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	spool, err := liveReplay(r.source, r.record)
	if err == nil {
		r.record = nil // ownership transfers to the fallback spool
	}
	return spool, err
}

func (r *InputReplay) Close() {
	if r.attempt != nil {
		_ = r.attempt.Close()
	}
	if r.record != nil {
		_ = r.record.Close()
	}
}

func privateReplayFile() (*os.File, error) {
	f, err := os.CreateTemp("", "ghok-delegate-stdin-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(f.Name()); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func liveReplay(source, prefix *os.File) (*Spool, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	info, err := source.Stat()
	if err != nil {
		_ = read.Close()
		_ = write.Close()
		return nil, err
	}
	return &Spool{source: source, replay: read, writer: write, prefix: prefix, supervised: true, probeFIFO: spoolProbeFIFO && info.Mode()&os.ModeNamedPipe != 0}, nil
}

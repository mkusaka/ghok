//go:build unix

package logging

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mkusaka/ghok/internal/redact"
)

const maxLogBytes int64 = 10 * 1024 * 1024

// Logger serializes goroutines with a mutex and processes with flock on a
// separate, persistent <path>.lock file. Never remove that lock file while
// writers may exist. The data file is opened anew under the lock for each
// record, so another process's rotation cannot leave a stale data descriptor.
// The zero value is closed; construct a Logger with Open or OpenWithRedactor.
type Logger struct {
	mu       sync.Mutex
	path     string
	level    string
	redactor *redact.Redactor
	lock     *os.File
}

// Open uses a snapshot of the process environment to learn secret values.
func Open(path string, level string) (*Logger, error) {
	return OpenWithRedactor(path, level, nil)
}

// OpenWithRedactor creates the log directory with mode 0700 and an append-only
// regular log file and persistent lock file with mode 0600. Existing directory
// and file modes are tightened as well. Symlink log/lock files and a symlink
// final directory are rejected. A nil redactor learns os.Environ(). An empty
// level means info; otherwise only info and decision are accepted.
func OpenWithRedactor(path string, level string, r *redact.Redactor) (_ *Logger, err error) {
	if path == "" {
		return nil, errors.New("logging: empty log path")
	}
	if level == "" {
		level = "info"
	}
	if level != "info" && level != "decision" {
		return nil, fmt.Errorf("logging: invalid level %q", level)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	_, statErr := os.Lstat(dir)
	created := errors.Is(statErr, os.ErrNotExist)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("logging: directory %q must not be a symlink", dir)
	}
	// Only tighten a directory we created (umask may have widened it); never
	// chmod a pre-existing, possibly shared, directory chosen by the user.
	if created {
		if err = os.Chmod(dir, 0700); err != nil {
			return nil, err
		}
	}
	if r == nil {
		env := make(map[string]string)
		for _, entry := range os.Environ() {
			key, value, ok := strings.Cut(entry, "=")
			if ok {
				env[key] = value
			}
		}
		r = redact.New(env)
	}
	lock, err := openPrivate(path + ".lock")
	if err != nil {
		return nil, err
	}
	if err = flock(lock, syscall.LOCK_EX); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	file, openErr := openPrivate(path)
	if file != nil {
		openErr = errors.Join(openErr, file.Close())
	}
	err = errors.Join(openErr, flock(lock, syscall.LOCK_UN))
	if err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return &Logger{path: path, level: level, redactor: r, lock: lock}, nil
}

// Write redacts and encodes one record, then takes the process lock around
// rotation and a single append write syscall. An existing file larger than
// 10 MiB is rotated before this write; the newest three archives are .1–.3.
// A record that crosses the limit remains whole and rotates on the next write.
// Zero timestamps are filled with the current UTC time. No console output is
// produced; callers decide whether to expose errors (normally only in debug).
func (l *Logger) Write(record Record) (err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lock == nil {
		return os.ErrClosed
	}
	if l.level == "decision" && !significant(record) {
		return nil
	}
	if record.Time.IsZero() {
		record.Time = time.Now().UTC()
	}
	data, err := json.Marshal(redactRecord(l.redactor, record))
	if err != nil {
		return fmt.Errorf("logging: encode record: %w", err)
	}
	data = append(data, '\n')
	if err = flock(l.lock, syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, flock(l.lock, syscall.LOCK_UN))
	}()
	file, err := openPrivate(l.path)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	if statErr != nil {
		return errors.Join(statErr, file.Close())
	}
	if info.Size() > maxLogBytes {
		if err = file.Close(); err != nil {
			return err
		}
		if err = rotate(l.path); err != nil {
			return err
		}
		file, err = openPrivate(l.path)
		if err != nil {
			return err
		}
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()
	n, err := syscall.Write(int(file.Fd()), data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

// Close releases the lock descriptor, without deleting the stable lock file.
// It is safe to call repeatedly or concurrently with Write.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lock == nil {
		return nil
	}
	err := l.lock.Close()
	l.lock = nil
	return err
}

func openPrivate(path string) (*os.File, error) {
	// O_NONBLOCK prevents opening a malicious FIFO from hanging the caller;
	// fstat below rejects every non-regular file. O_NOFOLLOW protects symlinks.
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_APPEND|
		syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("logging: %q is not a regular file", path)
	}
	if err == nil {
		err = file.Chmod(0600)
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func flock(file *os.File, operation int) error {
	for {
		err := syscall.Flock(int(file.Fd()), operation)
		if err != syscall.EINTR {
			return err
		}
	}
}

func rotate(path string) error {
	if err := os.Remove(path + ".3"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := 2; i >= 1; i-- {
		err := os.Rename(path+"."+strconv.Itoa(i), path+"."+strconv.Itoa(i+1))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(path, path+".1")
}

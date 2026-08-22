package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const logFileName = "gateway.log"

// RotatingWriter writes to gateway.log and rotates it before exceeding maxSize.
type RotatingWriter struct {
	dir        string
	maxSize    int64
	maxBackups int
	file       *os.File
	size       int64
	mu         sync.Mutex
}

func NewRotatingWriter(dir string, maxSize int64, maxBackups int) (*RotatingWriter, error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("log max size must be positive")
	}
	if maxBackups < 0 {
		return nil, fmt.Errorf("log max backups cannot be negative")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	path := filepath.Join(dir, logFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("stat log file: %w", err)
	}

	return &RotatingWriter{dir: dir, maxSize: maxSize, maxBackups: maxBackups, file: file, size: info.Size()}, nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("close log file for rotation: %w", err)
	}

	activePath := filepath.Join(w.dir, logFileName)
	archivePath := filepath.Join(w.dir, "gateway-"+time.Now().UTC().Format("20060102-150405.000000000")+".log")
	if err := os.Rename(activePath, archivePath); err != nil {
		return fmt.Errorf("archive log file: %w", err)
	}

	file, err := os.OpenFile(activePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open new log file: %w", err)
	}
	w.file = file
	w.size = 0
	return w.removeOldBackups()
}

func (w *RotatingWriter) removeOldBackups() error {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return fmt.Errorf("read log directory: %w", err)
	}

	var backups []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "gateway-") && strings.HasSuffix(entry.Name(), ".log") {
			backups = append(backups, entry.Name())
		}
	}
	sort.Strings(backups)
	for _, backup := range backups[:max(0, len(backups)-w.maxBackups)] {
		if err := os.Remove(filepath.Join(w.dir, backup)); err != nil {
			return fmt.Errorf("remove old log backup: %w", err)
		}
	}
	return nil
}

func NewWriter(dir string, maxSize int64, maxBackups int) (io.WriteCloser, error) {
	return NewRotatingWriter(dir, maxSize, maxBackups)
}

package logging

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingWriterRotatesAndKeepsBackups(t *testing.T) {
	dir := t.TempDir()
	w, err := NewRotatingWriter(dir, 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("5678")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("abcd")); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("files = %d, want 2", len(entries))
	}

	active, err := os.ReadFile(filepath.Join(dir, logFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(active) != "abcd" {
		t.Fatalf("active log = %q, want %q", active, "abcd")
	}
}

func TestNewRotatingWriterRejectsInvalidLimits(t *testing.T) {
	if _, err := NewRotatingWriter(t.TempDir(), 0, 1); err == nil {
		t.Fatal("expected max size error")
	}
	if _, err := NewRotatingWriter(t.TempDir(), 1, -1); err == nil {
		t.Fatal("expected max backups error")
	}
}

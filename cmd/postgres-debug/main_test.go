package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDBDSNFromSpecifiedFile(t *testing.T) {
	t.Setenv("DB_DSN", "")
	path := filepath.Join(t.TempDir(), "production.env")
	if err := os.WriteFile(path, []byte("OTHER=value\nDB_DSN=postgres://user:password@host/database\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadDBDSN(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("DB_DSN"); got != "postgres://user:password@host/database" {
		t.Fatalf("DB_DSN = %q", got)
	}
}

func TestLoadDBDSNRequiresConfiguredFileValue(t *testing.T) {
	t.Setenv("DB_DSN", "")
	path := filepath.Join(t.TempDir(), "production.env")
	if err := os.WriteFile(path, []byte("OTHER=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadDBDSN(path); err == nil {
		t.Fatal("loadDBDSN succeeded without DB_DSN")
	}
}

package store

import (
	"path/filepath"
	"testing"
)

func TestCopyTableReplacesExistingRows(t *testing.T) {
	source, err := New(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := New(filepath.Join(t.TempDir(), "destination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()

	if err := source.SaveProxySettings(ProxySettings{Provider: "openrouter", UseForChecker: true, Mode: "always"}); err != nil {
		t.Fatal(err)
	}
	if err := destination.SaveProxySettings(ProxySettings{Provider: "openrouter", UseForRequests: true}); err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 2; run++ {
		copied, err := copyTable(source.db, destination.db, "proxy_settings")
		if err != nil {
			t.Fatalf("copy run %d: %v", run, err)
		}
		if copied != 3 {
			t.Fatalf("copy run %d copied %d rows, want 3", run, copied)
		}
	}

	settings, err := destination.GetProxySettings("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if !settings.UseForChecker || settings.UseForRequests || settings.Mode != "always" {
		t.Fatalf("destination settings = %+v", settings)
	}
}

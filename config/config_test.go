package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndependentDatabaseName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SUI_DB_FOLDER", dir)
	if GetName() != "s-ui-ipv6-retry" || filepath.Base(GetDBPath()) != "s-ui-ipv6-retry.db" {
		t.Fatal("independent instance must use its own database name")
	}
}

func TestIsXrayDisabledFromEnvironment(t *testing.T) {
	t.Setenv("SUI_DB_FOLDER", t.TempDir())
	t.Setenv("SUI_DISABLE_XRAY", "yes")
	if !IsXrayDisabled() {
		t.Fatal("SUI_DISABLE_XRAY=yes did not disable Xray")
	}
}

func TestIsXrayDisabledFromMarker(t *testing.T) {
	dbDir := t.TempDir()
	t.Setenv("SUI_DB_FOLDER", dbDir)
	t.Setenv("SUI_DISABLE_XRAY", "")
	if err := os.WriteFile(filepath.Join(dbDir, ".disable_xray"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsXrayDisabled() {
		t.Fatal(".disable_xray marker did not disable Xray")
	}
}

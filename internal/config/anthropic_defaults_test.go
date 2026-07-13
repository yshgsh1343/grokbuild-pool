package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnthropicEnabledFalsePreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	body := "listen: \"127.0.0.1:18080\"\nadmin_key: \"local-dev-admin-key-not-for-prod\"\nanthropic:\n  enabled: false\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Anthropic.Enabled {
		t.Fatalf("expected anthropic.enabled=false to stick, got true")
	}
}

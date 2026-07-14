package catalog

import (
	"path/filepath"
	"testing"
	"time"
)

func TestModelCooldownPersist(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(filepath.Join(dir, "pool.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	until := time.Now().Add(10 * time.Minute).Unix()
	if err := c.UpsertModelCooldown("acc1", "grok-4.5", until, "upstream 429"); err != nil {
		t.Fatal(err)
	}
	rows, err := c.ListModelCooldowns("acc1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Model != "grok-4.5" || rows[0].RemainingSec <= 0 {
		t.Fatalf("rows=%+v", rows)
	}
	loaded, err := c.LoadActiveModelCooldowns(0)
	if err != nil {
		t.Fatal(err)
	}
	if loaded["acc1"]["grok-4.5"] != until {
		t.Fatalf("loaded=%v", loaded)
	}
}

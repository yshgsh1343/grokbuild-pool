package selector

import (
	"testing"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/hot"
)

func TestSecondaryStickyFailover(t *testing.T) {
	idx := hot.New(hot.Config{HotSize: 10, MaxInflightPerAccount: 2})
	a := catalog.HotMeta{ID: "a", Priority: 10, Enabled: true, Lifecycle: catalog.LifecycleActive}
	b := catalog.HotMeta{ID: "b", Priority: 10, Enabled: true, Lifecycle: catalog.LifecycleActive}
	c := catalog.HotMeta{ID: "c", Priority: 10, Enabled: true, Lifecycle: catalog.LifecycleActive}
	if _, err := idx.LoadMetas([]catalog.HotMeta{a, b, c}); err != nil {
		t.Fatal(err)
	}
	s := New(idx, Config{Strategy: StrategyStableRR, StickyTTLSec: 600, StickyMax: 100, JitterAmp: 0})
	now := int64(1_700_000_000)
	s.BindSticky("sess", "a")
	s.BindStickySecondary("sess", "b")
	// primary ok
	id, ok := s.Pick(now, "sess")
	if !ok || id != "a" {
		t.Fatalf("primary want a got %q ok=%v", id, ok)
	}
	// cool primary
	if meta, ok := idx.Get("a"); ok {
		meta.CooldownUntil = now + 100
		_, _ = idx.Promote(meta)
	}
	id, ok = s.Pick(now, "sess")
	if !ok || id != "b" {
		t.Fatalf("secondary want b got %q ok=%v", id, ok)
	}
}

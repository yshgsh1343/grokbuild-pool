package selector

import (
	"math/rand"
	"testing"

	"github.com/yshgsh1343/grokbuild2api/internal/catalog"
	"github.com/yshgsh1343/grokbuild2api/internal/hot"
)

func testIndex(t *testing.T, metas ...catalog.HotMeta) *hot.Index {
	t.Helper()
	idx := hot.New(hot.Config{HotSize: 100, MaxInflightPerAccount: 4})
	if _, err := idx.LoadMetas(metas); err != nil {
		t.Fatal(err)
	}
	return idx
}

func meta(id string, pri int32, inflight int32, fail float32) catalog.HotMeta {
	return catalog.HotMeta{
		ID:           id,
		Priority:     pri,
		Inflight:     inflight,
		FailureScore: fail,
		Enabled:      true,
		Lifecycle:    catalog.LifecycleActive,
	}
}

func TestScorePrefersLowInflightAndFailure(t *testing.T) {
	idx := testIndex(t, meta("a", 10, 0, 0), meta("b", 10, 3, 0), meta("c", 10, 0, 8))
	s := New(idx, Config{Pow2K: 3, WPriority: 1, WInflight: 10, WFailure: 5, JitterAmp: 0})
	s.SetRand(rand.New(rand.NewSource(1)))

	// 确定性：无抖动时 a 分最高
	if sa, sb := s.Score(meta("a", 10, 0, 0), 0), s.Score(meta("b", 10, 3, 0), 0); sa <= sb {
		t.Fatalf("low inflight should win: a=%v b=%v", sa, sb)
	}
	if sa, sc := s.Score(meta("a", 10, 0, 0), 0), s.Score(meta("c", 10, 0, 8), 0); sa <= sc {
		t.Fatalf("low failure should win: a=%v c=%v", sa, sc)
	}
}

func TestPickExcludesCooldownAndDisabled(t *testing.T) {
	now := int64(1_700_000_000)
	good := meta("good", 5, 0, 0)
	cool := meta("cool", 100, 0, 0)
	cool.CooldownUntil = now + 60
	off := meta("off", 100, 0, 0)
	off.Enabled = false
	idx := testIndex(t, good, cool, off)
	s := New(idx, Config{Pow2K: 3, JitterAmp: 0})
	s.SetRand(rand.New(rand.NewSource(42)))

	for i := 0; i < 20; i++ {
		id, ok := s.Pick(now, "")
		if !ok {
			t.Fatal("expected pick")
		}
		if id != "good" {
			t.Fatalf("iter %d: got %q want good", i, id)
		}
	}
}

func TestStickyPreferThenFailover(t *testing.T) {
	now := int64(1_700_000_000)
	a := meta("acct-a", 1, 0, 0)
	b := meta("acct-b", 1, 0, 0)
	idx := testIndex(t, a, b)
	s := New(idx, Config{Pow2K: 2, StickyTTLSec: 600, StickyMax: 100, JitterAmp: 0})
	s.SetRand(rand.New(rand.NewSource(7)))

	id1, ok := s.Pick(now, "sess-1")
	if !ok || id1 == "" {
		t.Fatal("first pick")
	}
	// 同一 sticky 应钉住
	for i := 0; i < 5; i++ {
		id, ok := s.Pick(now+int64(i), "sess-1")
		if !ok || id != id1 {
			t.Fatalf("sticky miss: got %q want %q", id, id1)
		}
	}
	// 排除粘性账号后应换号
	ex := map[string]struct{}{id1: {}}
	id2, ok := s.PickExcluding(now+10, "sess-1", ex)
	if !ok {
		t.Fatal("failover pick")
	}
	if id2 == id1 {
		t.Fatal("should not pick excluded sticky account")
	}
}

func BenchmarkPow2Pick(b *testing.B) {
	metas := make([]catalog.HotMeta, 0, 3000)
	for i := 0; i < 3000; i++ {
		metas = append(metas, catalog.HotMeta{
			ID:        "id-" + itoa(i),
			Priority:  int32(i % 10),
			Enabled:   true,
			Lifecycle: catalog.LifecycleActive,
		})
	}
	idx := hot.New(hot.Config{HotSize: 3000, MaxInflightPerAccount: 4})
	if _, err := idx.LoadMetas(metas); err != nil {
		b.Fatal(err)
	}
	s := New(idx, Config{Pow2K: 2, JitterAmp: 0.5})
	s.SetRand(rand.New(rand.NewSource(1)))
	now := int64(1_700_000_000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := s.Pick(now, ""); !ok {
			b.Fatal("empty pick")
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

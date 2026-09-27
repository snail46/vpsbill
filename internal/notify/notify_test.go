package notify

import "testing"

func TestCrossedThreshold(t *testing.T) {
	const gb = int64(1) << 30
	cases := []struct {
		used, limit int64
		want        int
		crossed     bool
	}{
		{79 * gb, 100 * gb, 0, false},
		{80 * gb, 100 * gb, 80, true},
		{100 * gb, 100 * gb, 100, true},
		{150 * gb, 100 * gb, 100, true},
		{10 * gb, 0, 0, false},
	}
	for _, c := range cases {
		got, crossed := crossedThreshold(c.used, c.limit, 80)
		if got != c.want || crossed != c.crossed {
			t.Errorf("crossedThreshold(%d, %d) = %d %v, want %d %v", c.used, c.limit, got, crossed, c.want, c.crossed)
		}
	}
}

func TestTrafficUsedBytesReadsProviderDocuments(t *testing.T) {
	if used, ok := TrafficUsedBytes(map[string]any{"total_used_bytes": int64(1234567890123)}); !ok || used != 1234567890123 {
		t.Fatalf("int document: %d %v", used, ok)
	}
	if used, ok := TrafficUsedBytes(map[string]any{"total_used_bytes": 2.5e9}); !ok || used != 2500000000 {
		t.Fatalf("float document: %d %v", used, ok)
	}
	if _, ok := TrafficUsedBytes(map[string]any{"rx_bytes": 1}); ok {
		t.Fatal("document without a total must be skipped")
	}
	if _, ok := TrafficUsedBytes(nil); ok {
		t.Fatal("nil document must be skipped")
	}
}

func TestExcerptCountsCharacters(t *testing.T) {
	if got := excerpt("  你好世界  ", 2); got != "你好…" {
		t.Fatalf("excerpt = %q", got)
	}
	if got := excerpt("short", 10); got != "short" {
		t.Fatalf("excerpt = %q", got)
	}
}

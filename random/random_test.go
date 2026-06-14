package random

import (
	"strings"
	"testing"
	"time"
)

// The before/after-now helpers must never sit on the wrong side of a time.Now()
// taken *after* them — that drift is exactly the date flaky they exist to kill.
// Looping hammers the randomness; the post-call now() stands in for a clock that
// kept moving while the test ran.

func TestADateBeforeNowAlwaysPast(t *testing.T) {
	for i := 0; i < 100_000; i++ {
		d := AnyDateBeforeNow()
		if !d.Before(time.Now()) {
			t.Fatalf("AnyDateBeforeNow returned %s, not before a later now", d)
		}
	}
}

func TestADateAfterNowAlwaysFuture(t *testing.T) {
	for i := 0; i < 100_000; i++ {
		d := AnyDateAfterNow()
		if !d.After(time.Now()) {
			t.Fatalf("AnyDateAfterNow returned %s, not after a later now", d)
		}
	}
}

func TestDateBeforeAfterRef(t *testing.T) {
	ref := time.Date(2020, 6, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 10_000; i++ {
		if d := DateBefore(ref); !d.Before(ref) {
			t.Fatalf("DateBefore returned %s, not before %s", d, ref)
		}
		if d := DateAfter(ref); !d.After(ref) {
			t.Fatalf("DateAfter returned %s, not after %s", d, ref)
		}
	}
}

func TestDateBetweenInRange(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2020, 12, 31, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10_000; i++ {
		d := DateBetween(start, end)
		if d.Before(start) || d.After(end) {
			t.Fatalf("DateBetween returned %s, outside [%s, %s]", d, start, end)
		}
	}
}

func TestDateBetweenPanicsWhenStartAfterEnd(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("DateBetween did not panic on start after end")
		}
	}()
	DateBetween(time.Now(), time.Now().Add(-time.Hour))
}

func TestDateBetweenEqualBounds(t *testing.T) {
	ref := time.Date(2020, 6, 1, 12, 0, 0, 0, time.UTC)
	if d := DateBetween(ref, ref); !d.Equal(ref) {
		t.Fatalf("DateBetween(ref, ref) = %s, want %s", d, ref)
	}
}

func TestPhraseRoundTrips(t *testing.T) {
	for i := 0; i < 1000; i++ {
		s := uint64(IntBetween(0, phraseMask))
		got, ok := parsePhrase(encodePhrase(s))
		if !ok || got != s {
			t.Fatalf("phrase round-trip failed for %d: encoded %q decoded (%d, %v)", s, encodePhrase(s), got, ok)
		}
	}
}

func TestParsePhraseRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "cloud", "cloud-river-fox", "not-a-real-word-here", "cloud-river-fox-amber-extra"} {
		if _, ok := parsePhrase(bad); ok {
			t.Errorf("parsePhrase(%q) accepted invalid phrase", bad)
		}
	}
}

func TestPhraseIsReadableWords(t *testing.T) {
	p := encodePhrase(0)
	if got := strings.Count(p, "-") + 1; got != phraseLen {
		t.Fatalf("phrase %q has %d words, want %d", p, got, phraseLen)
	}
}

func TestText(t *testing.T) {
	if Text(0) != "" || Text(-1) != "" {
		t.Fatal("Text(n<=0) should be empty")
	}
	if got := strings.Count(Text(5), " ") + 1; got != 5 {
		t.Fatalf("Text(5) word count = %d, want 5", got)
	}
}

package random

import (
	"strings"
	"testing"
)

func TestDrawHelpers(t *testing.T) {
	if n := Int(); n < 0 || n >= 1_000_000 {
		t.Errorf("Int out of range: %d", n)
	}
	if f := Float(); f < 0 || f >= 1 {
		t.Errorf("Float out of [0,1): %v", f)
	}
	if w := Word(); w == "" {
		t.Error("Word returned empty")
	}
	if got := strings.Count(Words(3), " ") + 1; got != 3 {
		t.Errorf("Words(3) word count = %d, want 3", got)
	}
	if FirstName() == "" || LastName() == "" {
		t.Error("name parts should be non-empty")
	}
	if e := Email(); !strings.Contains(e, "@") || !strings.Contains(e, ".") {
		t.Errorf("Email not well-formed: %q", e)
	}
	if p := Path(); !strings.HasPrefix(p, "/") {
		t.Errorf("Path should be absolute: %q", p)
	}
	if Date().IsZero() {
		t.Error("Date returned the zero time")
	}
}

func TestSeedAndPhraseDescribeSameSeed(t *testing.T) {
	if Phrase() == "" {
		t.Fatal("Phrase should be non-empty")
	}
	got, ok := parsePhrase(Phrase())
	if !ok || got != Seed()&phraseMask {
		t.Fatalf("Phrase %q does not round-trip to Seed %d", Phrase(), Seed())
	}
}

func TestSetSeedIsReproducible(t *testing.T) {
	defer saveSeedState()()

	forceResettable()
	SetSeed(12345)
	if Seed() != 12345 {
		t.Fatalf("Seed() = %d after SetSeed(12345)", Seed())
	}
	first := Int()

	forceResettable()
	SetSeed(12345)
	if got := Int(); got != first {
		t.Fatalf("SetSeed not reproducible: first draw %d, second %d", first, got)
	}
}

func TestSetSeedPanicsAfterDraw(t *testing.T) {
	defer saveSeedState()()

	mu.Lock()
	drawn = true
	seedFromEnv = false
	mu.Unlock()

	defer func() {
		if recover() == nil {
			t.Fatal("SetSeed should panic after a value was drawn")
		}
	}()
	SetSeed(1)
}

func TestSetSeedIgnoredWhenSeedFromEnv(t *testing.T) {
	defer saveSeedState()()

	mu.Lock()
	seed = 999
	gen = newGen(999)
	seedFromEnv = true
	drawn = false
	mu.Unlock()

	SetSeed(7) // ignored: an explicit env reproduction takes precedence.
	if Seed() != 999 {
		t.Fatalf("SetSeed should be ignored when seedFromEnv; Seed()=%d", Seed())
	}
}

func TestSetSeedPhrase(t *testing.T) {
	defer saveSeedState()()

	forceResettable()
	if err := SetSeedPhrase(encodePhrase(42)); err != nil {
		t.Fatalf("SetSeedPhrase rejected a valid phrase: %v", err)
	}
	if Seed() != 42&phraseMask {
		t.Fatalf("Seed() = %d after SetSeedPhrase for 42", Seed())
	}

	if err := SetSeedPhrase("not-a-valid-phrase"); err == nil {
		t.Fatal("SetSeedPhrase should error on a malformed phrase")
	}
}

// saveSeedState snapshots the package generator state and returns a restore
// func, so a white-box test can drive SetSeed without leaking into the rest of
// the suite's reproducible sequence.
func saveSeedState() func() {
	mu.Lock()
	s, g, d, e := seed, gen, drawn, seedFromEnv
	mu.Unlock()
	return func() {
		mu.Lock()
		seed, gen, drawn, seedFromEnv = s, g, d, e
		mu.Unlock()
	}
}

// forceResettable clears the write-once guards so SetSeed runs its happy path.
func forceResettable() {
	mu.Lock()
	drawn = false
	seedFromEnv = false
	mu.Unlock()
}

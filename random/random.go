// Package random builds throwaway test data.
package random

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var seed, seedFromEnv = initialSeed()

var (
	mu    sync.Mutex
	gen   = newGen(seed)
	drawn bool
)

func initialSeed() (uint64, bool) {
	if s := os.Getenv("TESTIGO_SEED"); s != "" {
		if v, err := strconv.ParseUint(s, 10, 64); err == nil {
			return v, true
		}
		if v, ok := parsePhrase(s); ok {
			return v, true
		}
	}
	return rand.Uint64() & phraseMask, false
}

func newGen(s uint64) *rand.Rand {
	return rand.New(rand.NewPCG(s, s^0x9e3779b97f4a7c15))
}

// Seed returns the seed driving this run.
func Seed() uint64 { return seed }

// SetSeed fixes the run's seed programmatically; panics if a value was already drawn.
func SetSeed(s uint64) {
	mu.Lock()
	defer mu.Unlock()
	if seedFromEnv {
		return
	}
	if drawn {
		panic("random: SetSeed called after a value was already drawn; call it from TestMain before any draw")
	}
	seed = s
	gen = newGen(s)
}

// SetSeedPhrase is SetSeed taking the readable word phrase form.
func SetSeedPhrase(phrase string) error {
	s, ok := parsePhrase(phrase)
	if !ok {
		return fmt.Errorf("random: %q is not a valid seed phrase", phrase)
	}
	SetSeed(s)
	return nil
}

// Replay runs fn with the generator temporarily rebuilt from the given seed.
func Replay(seed uint64, fn func()) {
	mu.Lock()
	prev := gen
	gen = newGen(seed)
	mu.Unlock()
	defer func() {
		mu.Lock()
		gen = prev
		mu.Unlock()
	}()
	fn()
}

func intN(n int) int       { mu.Lock(); defer mu.Unlock(); drawn = true; return gen.IntN(n) }
func int64N(n int64) int64 { mu.Lock(); defer mu.Unlock(); drawn = true; return gen.Int64N(n) }
func floatV() float64      { mu.Lock(); defer mu.Unlock(); drawn = true; return gen.Float64() }

// Int returns a non-negative random int, small enough to read in a failure
// message yet wide enough to avoid collisions across a test.
func Int() int {
	return intN(1_000_000)
}

// IntBetween returns a random int in [min, max]. It panics when min > max.
func IntBetween(min, max int) int {
	if min > max {
		panic(fmt.Sprintf("random: IntBetween(%d, %d): min greater than max", min, max))
	}
	return min + intN(max-min+1)
}

// Float returns a random float64 in [0, 1).
func Float() float64 {
	return floatV()
}

// Bool returns a random bool.
func Bool() bool {
	return intN(2) == 1
}

// ID returns a random positive identifier, never zero, for fields that use the
// zero value as "unset".
func ID() int {
	return 1 + intN(1_000_000)
}

// Word returns a single random lowercase word.
func Word() string {
	return words[intN(len(words))]
}

// Words returns n random words joined by spaces.
func Words(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = Word()
	}
	return strings.Join(parts, " ")
}

// Name returns a random "First Last" full name.
func Name() string {
	return firstNames[intN(len(firstNames))] + " " + lastNames[intN(len(lastNames))]
}

// FirstName returns a random first name.
func FirstName() string {
	return firstNames[intN(len(firstNames))]
}

// LastName returns a random last name.
func LastName() string {
	return lastNames[intN(len(lastNames))]
}

// Email returns a random, syntactically valid email address.
func Email() string {
	return fmt.Sprintf("%s.%s%d@%s",
		strings.ToLower(FirstName()),
		strings.ToLower(LastName()),
		intN(1000),
		domains[intN(len(domains))],
	)
}

// Path returns a random absolute Unix-style path with two or three segments.
func Path() string {
	depth := 2 + intN(2)
	parts := make([]string, depth)
	for i := range parts {
		parts[i] = Word()
	}
	return "/" + strings.Join(parts, "/")
}

// String returns a random lowercase alphabetic string of length n.
func String(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[intN(len(alphabet))]
	}
	return string(b)
}

const dateMargin = time.Hour

const dateSpan = 365 * 24 * time.Hour

var now = time.Now

// Date returns a random time within roughly a year on either side of now.
func Date() time.Time {
	offset := time.Duration(int64N(int64(2*dateSpan))) - dateSpan
	return now().Add(offset)
}

// DateBetween returns a random time in [start, end]. It panics when start is
// after end.
func DateBetween(start, end time.Time) time.Time {
	if start.After(end) {
		panic(fmt.Sprintf("random: DateBetween(%s, %s): start after end", start, end))
	}
	span := end.Sub(start)
	if span <= 0 {
		return start
	}
	return start.Add(time.Duration(int64N(int64(span) + 1)))
}

// DateBefore returns a random time strictly before ref.
func DateBefore(ref time.Time) time.Time {
	return ref.Add(-dateMargin - time.Duration(int64N(int64(dateSpan))))
}

// DateAfter returns a random time strictly after ref.
func DateAfter(ref time.Time) time.Time {
	return ref.Add(dateMargin + time.Duration(int64N(int64(dateSpan))))
}

// AnyDateBeforeNow returns a random time safely in the past.
func AnyDateBeforeNow() time.Time {
	return DateBefore(now())
}

// AnyDateAfterNow returns a random time safely in the future.
func AnyDateAfterNow() time.Time {
	return DateAfter(now())
}

// Pick returns one of options at random. It panics when options is empty.
func Pick[T any](options ...T) T {
	if len(options) == 0 {
		panic("random: Pick needs at least one option")
	}
	return options[intN(len(options))]
}

var (
	firstNames = []string{"Ada", "Alan", "Grace", "Linus", "Dennis", "Ken", "Barbara", "Margaret", "Edsger", "Tony", "Donald", "Niklaus"}
	lastNames  = []string{"Lovelace", "Turing", "Hopper", "Torvalds", "Ritchie", "Thompson", "Liskov", "Hamilton", "Dijkstra", "Hoare", "Knuth", "Wirth"}
	domains    = []string{"example.com", "test.dev", "mail.local", "acme.io"}
	words      = []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet", "kilo", "lima", "mike", "november"}
)

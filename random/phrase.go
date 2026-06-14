package random

import "strings"

const (
	phraseBits = 6
	phraseLen  = 4
	phraseMask = 1<<(phraseBits*phraseLen) - 1
)

var phraseWords = []string{
	"amber", "anchor", "apple", "arrow", "aspen", "basil", "beacon", "birch",
	"bison", "bloom", "brook", "cedar", "clay", "cliff", "cloud", "clover",
	"comet", "coral", "cove", "crane", "crest", "dawn", "delta", "dune",
	"ember", "fern", "flint", "fox", "frost", "glade", "grove", "harbor",
	"hazel", "heron", "ivy", "jade", "lark", "leaf", "lily", "lotus",
	"maple", "marsh", "meadow", "mist", "moss", "oak", "opal", "otter",
	"pebble", "pine", "quartz", "raven", "reed", "ridge", "river", "robin",
	"sage", "shell", "slate", "spruce", "stone", "storm", "tide", "willow",
}

var phraseIndex = func() map[string]uint64 {
	m := make(map[string]uint64, len(phraseWords))
	for i, w := range phraseWords {
		m[w] = uint64(i)
	}
	return m
}()

// Text returns n random words from the seed-phrase vocabulary.
func Text(n int) string {
	if n <= 0 {
		return ""
	}
	w := make([]string, n)
	for i := range w {
		w[i] = phraseWords[intN(len(phraseWords))]
	}
	return strings.Join(w, " ")
}

// Phrase renders the run's seed as its word phrase.
func Phrase() string { return encodePhrase(seed) }

func encodePhrase(s uint64) string {
	s &= phraseMask
	w := make([]string, phraseLen)
	for i := 0; i < phraseLen; i++ {
		w[i] = phraseWords[s&(1<<phraseBits-1)]
		s >>= phraseBits
	}
	return strings.Join(w, "-")
}

func parsePhrase(p string) (uint64, bool) {
	parts := strings.Split(p, "-")
	if len(parts) != phraseLen {
		return 0, false
	}
	var s uint64
	for i := phraseLen - 1; i >= 0; i-- {
		idx, ok := phraseIndex[parts[i]]
		if !ok {
			return 0, false
		}
		s = s<<phraseBits | idx
	}
	return s, true
}

package fixture

import (
	"strconv"
	"testing"

	"github.com/lautaromei/testigo/assert"
	"github.com/lautaromei/testigo/random"
)

// widget carries a slice, so a shallow copy would alias it — the tests use that
// to prove every build hands out a deep, independent copy.
type widget struct {
	name string
	tags []string
	id   int
	flag bool
}

func setName(name string) func(widget) widget {
	return func(w widget) widget {
		w.name = name
		return w
	}
}

func addTag(tag string) func(widget) widget {
	return func(w widget) widget {
		w.tags = append(w.tags, tag)
		return w
	}
}

func TestWith(t *testing.T) {
	t.Run("applies mutators in order", func(t *testing.T) {
		got := With(widget{}, setName("a"), setName("b"))

		assert.Equal(t, got.name, "b")
	})

	t.Run("skips nil mutators", func(t *testing.T) {
		got := With(widget{name: "seed"}, nil, setName("set"), nil)

		assert.Equal(t, got.name, "set")
	})

	t.Run("with no mutators returns a copy of the base", func(t *testing.T) {
		got := With(widget{name: "seed"})

		assert.Equal(t, got.name, "seed")
	})

	t.Run("deep-copies the base, so a mutator can't alias it", func(t *testing.T) {
		base := widget{tags: []string{"orig"}}

		got := With(base, func(w widget) widget {
			w.tags[0] = "changed"
			return w
		})

		assert.Equal(t, got.tags[0], "changed")
		assert.Equal(t, base.tags[0], "orig") // base's slice untouched
	})
}

func TestNew_With(t *testing.T) {
	deluxe := New(widget{name: "deluxe", tags: []string{"base"}})

	t.Run("builds the base with extra mutators", func(t *testing.T) {
		got := deluxe.With(addTag("extra"))

		assert.Equal(t, got.tags, []string{"base", "extra"})
	})

	t.Run("repeated builds are independent", func(t *testing.T) {
		first := deluxe.With(addTag("one"))
		second := deluxe.With(addTag("two"))

		assert.Equal(t, first.tags, []string{"base", "one"})
		assert.Equal(t, second.tags, []string{"base", "two"}) // first's append didn't leak
	})
}

func TestEmpty(t *testing.T) {
	got := Empty[widget]().With(setName("built"))

	assert.Equal(t, got.name, "built")
	assert.Equal(t, Empty[widget]().Bare(), widget{}) // zero value
}

func TestBare(t *testing.T) {
	base := widget{name: "seed", tags: []string{"orig"}}
	deluxe := New(base)

	bare := deluxe.Bare()
	bare.tags[0] = "changed"

	assert.Equal(t, base.tags[0], "orig") // the Base's value is untouched
}

func TestPtr(t *testing.T) {
	deluxe := New(widget{name: "seed"})

	got := deluxe.Ptr(setName("built"))

	assert.Equal(t, got.name, "built")
	assert.Equal(t, deluxe.Bare().name, "seed") // pointee is an independent copy
}

func TestTimes(t *testing.T) {
	deluxe := New(widget{tags: []string{"base"}})

	got := deluxe.Times(3, addTag("t"))

	assert.Len(t, got, 3)
	assert.Equal(t, got[0].tags, []string{"base", "t"})
	assert.Equal(t, got[2].tags, []string{"base", "t"}) // same recipe each
	got[0].tags[1] = "mutated"
	assert.Equal(t, got[1].tags[1], "t") // elements are independent copies
}

func TestSlice(t *testing.T) {
	deluxe := New(widget{tags: []string{"base"}})

	got := deluxe.Slice(3, func(i int) []func(widget) widget {
		return []func(widget) widget{setName(strconv.Itoa(i)), addTag("t")}
	})

	t.Run("builds n elements, each from its own mutators", func(t *testing.T) {
		assert.Len(t, got, 3)
		assert.Equal(t, got[0].name, "0")
		assert.Equal(t, got[2].name, "2")
	})

	t.Run("elements are independent copies", func(t *testing.T) {
		assert.Equal(t, got[0].tags, []string{"base", "t"})
		assert.Equal(t, got[1].tags, []string{"base", "t"}) // element 0's append didn't leak
	})
}

func TestOneOf(t *testing.T) {
	t.Run("applies exactly one of the mutators, and varies across runs", func(t *testing.T) {
		pick := OneOf(setName("a"), setName("b"), setName("c"))

		seen := map[string]bool{}
		for i := 0; i < 200; i++ {
			seen[New(widget{}).With(pick).name] = true
		}

		for name := range seen {
			assert.Contains(t, []string{"a", "b", "c"}, name) // always one of them
		}
		assert.True(t, len(seen) > 1, "expected more than one variant across runs")
	})

	t.Run("with no mutators leaves the value unchanged", func(t *testing.T) {
		got := With(widget{name: "seed"}, OneOf[widget]())

		assert.Equal(t, got.name, "seed")
	})
}

func TestMaybe(t *testing.T) {
	maybeTag := Maybe(addTag("x"))

	withTag, without := false, false
	for i := 0; i < 200; i++ {
		got := New(widget{}).With(maybeTag)
		switch len(got.tags) {
		case 0:
			without = true
		case 1:
			assert.Equal(t, got.tags[0], "x")
			withTag = true
		}
	}

	assert.True(t, withTag, "expected the mutator to apply on some runs")
	assert.True(t, without, "expected the mutator to skip on some runs")
}

func TestReplay(t *testing.T) {
	randomWidget := func() widget {
		return New(widget{}).With(func(w widget) widget {
			w.name = random.String(8)
			return w
		})
	}

	t.Run("the same seed rebuilds the exact same value", func(t *testing.T) {
		first := Replay(42, randomWidget)
		second := Replay(42, randomWidget)

		assert.Equal(t, first.name, second.name)
	})

	t.Run("a different seed draws a different value", func(t *testing.T) {
		assert.NotEqual(t, Replay(1, randomWidget).name, Replay(2, randomWidget).name)
	})

	t.Run("pins every random.* kind, including ints and bools", func(t *testing.T) {
		// One build drawing several different random kinds — all replay together.
		mix := func() widget {
			return New(widget{}).With(func(w widget) widget {
				w.name = random.String(6)
				w.tags = []string{random.Name()}
				w.id = random.ID()
				w.flag = random.Bool()
				return w
			})
		}

		assert.Equal(t, Replay(99, mix), Replay(99, mix))
	})

	t.Run("pins a whole batch, not just one value", func(t *testing.T) {
		deluxe := New(widget{})
		build := func() []widget {
			return deluxe.Times(3, func(w widget) widget {
				w.id = random.ID()
				return w
			})
		}

		first := Replay(55, build)
		second := Replay(55, build)

		assert.Len(t, first, 3)
		assert.Equal(t, first, second) // every element of the batch replays
	})
}

func TestCopy(t *testing.T) {
	base := widget{tags: []string{"orig"}}
	original := New(base)

	forked := original.Copy()
	forked.Bare().tags[0] = "changed"

	assert.Equal(t, original.Bare().tags[0], "orig") // fork shares no memory
}

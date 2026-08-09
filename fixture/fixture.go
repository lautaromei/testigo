// Package fixture builds reusable test values from a canonical base, either as
// direct variants or through immutable fluent builders.
package fixture

import (
	"github.com/lautaromei/testigo"
	"github.com/lautaromei/testigo/random"
)

// Variation derives a variant.
type Variation[T any] = func(T) T

// Configuration mutates a private copy held by a Builder.
type Configuration[T any] = func(*T)

// With returns a deep copy of base with each variation applied in order.
func With[T any](base T, variations ...Variation[T]) T {
	v := testigo.Copy(base)
	for _, vary := range variations {
		if vary != nil {
			v = vary(v)
		}
	}
	return v
}

// OneOf returns a variation that applies one of the given variations, chosen at random.
func OneOf[T any](variations ...Variation[T]) Variation[T] {
	return func(v T) T {
		if len(variations) == 0 {
			return v
		}
		if m := random.Pick(variations...); m != nil {
			v = m(v)
		}
		return v
	}
}

// Maybe returns a variation that applies it on roughly half of runs.
func Maybe[T any](variation Variation[T]) Variation[T] {
	return func(v T) T {
		if variation != nil && random.Bool() {
			v = variation(v)
		}
		return v
	}
}

// Replay runs build with random draws pinned to seed.
func Replay[T any](seed uint64, build func() T) T {
	var out T
	random.Replay(seed, func() { out = build() })
	return out
}

// Base is a reusable, named seed value that hands out a fresh deep copy on every With.
type Base[T any] struct {
	base T
}

// New wraps a canonical base value in a Base.
func New[T any](base T) Base[T] {
	return Base[T]{base: base}
}

// Empty wraps the zero value of T in a Base.
func Empty[T any]() Base[T] {
	var zero T
	return Base[T]{base: zero}
}

// Builder returns an immutable fluent builder starting from a fresh copy of
// the base. Each chained operation returns independent state.
func (f Base[T]) Builder() Builder[T] {
	return NewBuilder(f.base)
}

// With returns a fresh variant: a deep copy with variations applied.
func (f Base[T]) With(variations ...Variation[T]) T {
	return With(f.base, variations...)
}

// Bare returns a fresh deep copy of the canonical base value.
func (f Base[T]) Bare() T {
	return testigo.Copy(f.base)
}

// Ptr returns a pointer to the freshly built variant.
func (f Base[T]) Ptr(variations ...Variation[T]) *T {
	v := f.With(variations...)
	return &v
}

// Times builds n independent variants.
func (f Base[T]) Times(n int, variations ...Variation[T]) []T {
	out := make([]T, n)
	for i := range out {
		out[i] = f.With(variations...)
	}
	return out
}

// Slice builds n independent variants, each defined by index.
func (f Base[T]) Slice(n int, each func(i int) []Variation[T]) []T {
	out := make([]T, n)
	for i := range out {
		out[i] = f.With(each(i)...)
	}
	return out
}

// Copy returns an independent copy of the Base.
func (f Base[T]) Copy() Base[T] {
	return Base[T]{base: testigo.Copy(f.base)}
}

// Builder derives values fluently without mutating its source or sibling
// builders. Finish a chain with Bare.
type Builder[T any] struct {
	value T
}

// NewBuilder starts an immutable fluent builder from base.
func NewBuilder[T any](base T) Builder[T] {
	return Builder[T]{value: testigo.Copy(base)}
}

// Set applies pointer-based configurations to a fresh copy and returns the
// resulting builder. Nil configurations are ignored.
func (b Builder[T]) Set(configurations ...Configuration[T]) Builder[T] {
	next := testigo.Copy(b.value)
	for _, configure := range configurations {
		if configure != nil {
			configure(&next)
		}
	}
	return Builder[T]{value: next}
}

// With applies functional variations to a fresh copy and returns the resulting
// builder.
func (b Builder[T]) With(variations ...Variation[T]) Builder[T] {
	return Builder[T]{value: With(b.value, variations...)}
}

// Bare returns a fresh deep copy of the builder's current value.
func (b Builder[T]) Bare() T {
	return testigo.Copy(b.value)
}

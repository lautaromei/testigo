// Package seed persists the data a test needs as its initial condition.
//
// A repository is adapted with a small func(T) error callback, so seed does not
// impose a repository interface or make assumptions about contexts and return
// values. Generate and GenerateOne accept a typed Random implementation; using
// testigo/random inside Random makes the generated records reproducible.
package seed

import "testing"

// Random generates a random value of T. Domain types may implement Random
// directly in same-package tests; types from another package can be generated
// by a small local adapter that implements this interface.
type Random[T any] interface {
	Random() T
}

// One persists value with upsert and returns the same value for use by the
// test. A persistence error aborts the test because its initial condition could
// not be established.
func One[T any](t testing.TB, upsert func(T) error, value T) T {
	t.Helper()
	requireUpsert(t, upsert)

	if err := upsert(value); err != nil {
		t.Fatalf("seed: cannot persist %T: %v", value, err)
	}
	return value
}

// Many persists values in order with upsert and returns them for use by the
// test. It stops at the first persistence error and aborts the test.
func Many[T any](t testing.TB, upsert func(T) error, values ...T) []T {
	t.Helper()
	requireUpsert(t, upsert)

	for index, value := range values {
		if err := upsert(value); err != nil {
			t.Fatalf("seed: cannot persist item %d of type %T: %v", index, value, err)
		}
	}
	return values
}

// GenerateOne asks randomizer for one value, persists it, and returns it.
// Random values drawn through testigo/random are reproducible.
func GenerateOne[T any](t testing.TB, upsert func(T) error, randomizer Random[T]) T {
	t.Helper()
	requireRandom(t, randomizer)

	return One(t, upsert, randomizer.Random())
}

// Generate asks randomizer for count values, persists them in order, and
// returns them. Random values drawn through testigo/random are reproducible.
func Generate[T any](t testing.TB, count int, upsert func(T) error, randomizer Random[T]) []T {
	t.Helper()
	requireRandom(t, randomizer)

	return GenerateWith(t, count, upsert, func(int) T {
		return randomizer.Random()
	})
}

// GenerateOneWith builds, persists, and returns one value. It is the factory
// variant of GenerateOne for values that do not implement Random.
func GenerateOneWith[T any](t testing.TB, upsert func(T) error, build func() T) T {
	t.Helper()
	requireUpsert(t, upsert)
	if build == nil {
		t.Fatal("seed: nil builder function")
	}

	return One(t, upsert, build())
}

// GenerateWith builds and persists count values in order, returning every
// value. The index lets build derive stable per-record fields. It is the
// factory variant of Generate for values that do not implement Random.
func GenerateWith[T any](t testing.TB, count int, upsert func(T) error, build func(index int) T) []T {
	t.Helper()
	if count < 0 {
		t.Fatalf("seed: count must be non-negative, got %d", count)
	}
	requireUpsert(t, upsert)
	if build == nil {
		t.Fatal("seed: nil builder function")
	}

	values := make([]T, 0, count)
	for index := range count {
		value := build(index)
		if err := upsert(value); err != nil {
			t.Fatalf("seed: cannot persist generated item %d of type %T: %v", index, value, err)
		}
		values = append(values, value)
	}
	return values
}

func requireRandom[T any](t testing.TB, randomizer Random[T]) {
	t.Helper()
	if randomizer == nil {
		t.Fatal("seed: nil randomizer")
	}
}

func requireUpsert[T any](t testing.TB, upsert func(T) error) {
	t.Helper()
	if upsert == nil {
		t.Fatal("seed: nil upsert function")
	}
}

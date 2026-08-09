// Package seed persists the data a test needs as its initial condition.
//
// A repository is adapted with a small func(T) error callback, so seed does not
// impose a repository interface or make assumptions about contexts and return
// values. Generate and GenerateOne accept typed generator functions; using
// testigo/random inside a generator makes the generated records reproducible.
package seed

import "testing"

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

// GenerateOne builds, persists, and returns one value. Random values drawn by
// generate through testigo/random are reproducible.
func GenerateOne[T any](t testing.TB, upsert func(T) error, generate func() T) T {
	t.Helper()
	requireUpsert(t, upsert)
	requireGenerator(t, generate)

	return One(t, upsert, generate())
}

// Generate builds and persists count values in order, returning every value.
// Random values drawn by generate through testigo/random are reproducible.
func Generate[T any](t testing.TB, count int, upsert func(T) error, generate func() T) []T {
	t.Helper()
	if count < 0 {
		t.Fatalf("seed: count must be non-negative, got %d", count)
	}
	requireUpsert(t, upsert)
	requireGenerator(t, generate)

	values := make([]T, 0, count)
	for index := range count {
		value := generate()
		if err := upsert(value); err != nil {
			t.Fatalf("seed: cannot persist generated item %d of type %T: %v", index, value, err)
		}
		values = append(values, value)
	}
	return values
}

func requireGenerator[T any](t testing.TB, generate func() T) {
	t.Helper()
	if generate == nil {
		t.Fatal("seed: nil generator function")
	}
}

func requireUpsert[T any](t testing.TB, upsert func(T) error) {
	t.Helper()
	if upsert == nil {
		t.Fatal("seed: nil upsert function")
	}
}

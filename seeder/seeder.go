// Package seeder persists the data a test needs as its initial condition.
//
// A Seeder captures the test and a typed persistence function once. It can
// persist explicit values, generated values, or any fixture builder exposing
// Bare() T without imposing a repository interface.
package seeder

import "testing"

// Buildable is a fixture or builder that returns a fresh value through Bare.
type Buildable[T any] interface {
	Bare() T
}

// Seeder persists initial values of T for one test.
type Seeder[T any] struct {
	t       testing.TB
	persist func(T) error
}

// New creates a typed Seeder. Contexts and repository-specific return values
// can be adapted once in persist's closure.
func New[T any](t testing.TB, persist func(T) error) Seeder[T] {
	t.Helper()
	if persist == nil {
		t.Fatal("seeder: nil persist function")
	}
	return Seeder[T]{t: t, persist: persist}
}

// One persists value and returns it. A persistence error aborts the test
// because its initial condition could not be established.
func (s Seeder[T]) One(value T) T {
	s.t.Helper()
	if err := s.persist(value); err != nil {
		s.t.Fatalf("seeder: cannot persist %T: %v", value, err)
	}
	return value
}

// Many persists values in order and returns them. It stops at the first
// persistence error and aborts the test.
func (s Seeder[T]) Many(values ...T) []T {
	s.t.Helper()
	for index, value := range values {
		if err := s.persist(value); err != nil {
			s.t.Fatalf("seeder: cannot persist item %d of type %T: %v", index, value, err)
		}
	}
	return values
}

// GenerateOne builds, persists, and returns one value. Random values drawn by
// generate through testigo/random are reproducible.
func (s Seeder[T]) GenerateOne(generate func() T) T {
	s.t.Helper()
	s.requireGenerator(generate)
	return s.One(generate())
}

// Generate builds and persists count values in order, returning every value.
// Random values drawn by generate through testigo/random are reproducible.
func (s Seeder[T]) Generate(count int, generate func() T) []T {
	s.t.Helper()
	if count < 0 {
		s.t.Fatalf("seeder: count must be non-negative, got %d", count)
	}
	s.requireGenerator(generate)

	values := make([]T, 0, count)
	for index := range count {
		value := generate()
		if err := s.persist(value); err != nil {
			s.t.Fatalf("seeder: cannot persist generated item %d of type %T: %v", index, value, err)
		}
		values = append(values, value)
	}
	return values
}

// Persist builds one fresh value through Bare, persists it, and returns it.
func (s Seeder[T]) Persist(builder Buildable[T]) T {
	s.t.Helper()
	if builder == nil {
		s.t.Fatal("seeder: nil builder")
	}
	return s.One(builder.Bare())
}

func (s Seeder[T]) requireGenerator(generate func() T) {
	s.t.Helper()
	if generate == nil {
		s.t.Fatal("seeder: nil generator function")
	}
}

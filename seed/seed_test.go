package seed

import (
	"testing"

	"github.com/lautaromei/testigo/assert"
	"github.com/lautaromei/testigo/fixture"
	"github.com/lautaromei/testigo/random"
)

type user struct {
	ID    int
	Name  string
	Email string
}

func (user) Random() user {
	return user{
		ID:    random.ID(),
		Name:  random.Name(),
		Email: random.Email(),
	}
}

func TestOnePersistsAndReturnsValue(t *testing.T) {
	var persisted []user
	upsert := func(value user) error {
		persisted = append(persisted, value)
		return nil
	}
	want := user{ID: 1, Name: "Ada"}

	got := One(t, upsert, want)

	assert.Equal(t, got, want)
	assert.Equal(t, persisted, []user{want})
}

func TestManyPersistsInOrderAndReturnsValues(t *testing.T) {
	var persisted []user
	upsert := func(value user) error {
		persisted = append(persisted, value)
		return nil
	}
	want := []user{{ID: 1}, {ID: 2}, {ID: 3}}

	got := Many(t, upsert, want...)

	assert.Equal(t, got, want)
	assert.Equal(t, persisted, want)
}

func TestGenerateBuildsAndPersistsValuesInOrder(t *testing.T) {
	var persisted []user
	upsert := func(value user) error {
		persisted = append(persisted, value)
		return nil
	}

	got := Generate(t, 3, upsert, user{})

	assert.Len(t, got, 3)
	assert.Equal(t, got, persisted)
	assert.Positive(t, got[0].ID)
	assert.NotEmpty(t, got[0].Name)
	assert.NotEmpty(t, got[0].Email)
}

func TestGenerateOneUsesRandomInterface(t *testing.T) {
	var persisted user

	got := GenerateOne(t, func(value user) error {
		persisted = value
		return nil
	}, user{})

	assert.Equal(t, got, persisted)
	assert.Positive(t, got.ID)
	assert.NotEmpty(t, got.Name)
	assert.NotEmpty(t, got.Email)
}

func TestGenerateOneWithCombinesWithFixture(t *testing.T) {
	users := fixture.New(user{Name: "Ada"})
	var persisted user

	got := GenerateOneWith(t, func(value user) error {
		persisted = value
		return nil
	}, func() user {
		return users.With(func(value user) user {
			value.ID = random.ID()
			value.Email = random.Email()
			return value
		})
	})

	assert.Equal(t, got, persisted)
	assert.Equal(t, got.Name, "Ada")
	assert.Positive(t, got.ID)
	assert.NotEmpty(t, got.Email)
}

func TestManyAcceptsNoValues(t *testing.T) {
	calls := 0

	got := Many(t, func(user) error {
		calls++
		return nil
	})

	assert.Len(t, got, 0)
	assert.Equal(t, calls, 0)
}

func TestGenerateAcceptsZeroCount(t *testing.T) {
	upserts := 0

	got := Generate(t, 0, func(user) error {
		upserts++
		return nil
	}, user{})

	assert.Len(t, got, 0)
	assert.Equal(t, upserts, 0)
}

func TestGenerateWithProvidesEachIndex(t *testing.T) {
	builds := 0
	upserts := 0

	got := GenerateWith(t, 3, func(user) error {
		upserts++
		return nil
	}, func(index int) user {
		builds++
		return user{ID: index + 1}
	})

	assert.Equal(t, got, []user{{ID: 1}, {ID: 2}, {ID: 3}})
	assert.Equal(t, builds, 3)
	assert.Equal(t, upserts, 3)
}

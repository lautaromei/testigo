package seeder

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

func TestOnePersistsAndReturnsValue(t *testing.T) {
	var persisted []user
	users := New(t, func(value user) error {
		persisted = append(persisted, value)
		return nil
	})
	want := user{ID: 1, Name: "Ada"}

	got := users.One(want)

	assert.Equal(t, got, want)
	assert.Equal(t, persisted, []user{want})
}

func TestManyPersistsInOrderAndReturnsValues(t *testing.T) {
	var persisted []user
	users := New(t, func(value user) error {
		persisted = append(persisted, value)
		return nil
	})
	want := []user{{ID: 1}, {ID: 2}, {ID: 3}}

	got := users.Many(want...)

	assert.Equal(t, got, want)
	assert.Equal(t, persisted, want)
}

func TestGenerateBuildsAndPersistsValuesInOrder(t *testing.T) {
	var persisted []user
	generated := 0
	users := New(t, func(value user) error {
		persisted = append(persisted, value)
		return nil
	})

	got := users.Generate(3, func() user {
		generated++
		return user{ID: generated, Name: random.Name(), Email: random.Email()}
	})

	assert.Len(t, got, 3)
	assert.Equal(t, got, persisted)
	assert.Equal(t, []int{got[0].ID, got[1].ID, got[2].ID}, []int{1, 2, 3})
	assert.NotEmpty(t, got[0].Name)
	assert.NotEmpty(t, got[0].Email)
}

func TestGenerateOneCombinesWithFixture(t *testing.T) {
	base := fixture.New(user{Name: "Ada"})
	var persisted user
	users := New(t, func(value user) error {
		persisted = value
		return nil
	})

	got := users.GenerateOne(func() user {
		return base.With(func(value user) user {
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

func TestPersistCallsBareAndPersistsItsFreshValue(t *testing.T) {
	base := fixture.NewBuilder(user{ID: 1, Name: "base"})
	builder := base.Set(func(value *user) {
		value.Name = "Ada"
	})
	var persisted user
	users := New(t, func(value user) error {
		persisted = value
		return nil
	})

	got := users.Persist(builder)

	assert.Equal(t, got, user{ID: 1, Name: "Ada"})
	assert.Equal(t, persisted, got)
	assert.Equal(t, base.Bare(), user{ID: 1, Name: "base"})
}

func TestManyAcceptsNoValues(t *testing.T) {
	calls := 0
	users := New(t, func(user) error {
		calls++
		return nil
	})

	got := users.Many()

	assert.Len(t, got, 0)
	assert.Equal(t, calls, 0)
}

func TestGenerateAcceptsZeroCount(t *testing.T) {
	builds := 0
	upserts := 0
	users := New(t, func(user) error {
		upserts++
		return nil
	})

	got := users.Generate(0, func() user {
		builds++
		return user{}
	})

	assert.Len(t, got, 0)
	assert.Equal(t, builds, 0)
	assert.Equal(t, upserts, 0)
}

package testkit

import (
	"context"
	"testing"

	"github.com/lautaromei/testigo"
	"github.com/lautaromei/testigo/assert"
	"github.com/lautaromei/testigo/internal/testigogen/testdata/domain"
)

type repository struct {
	rows []domain.User
}

func (r *repository) Upsert(_ context.Context, user domain.User) (domain.User, error) {
	r.rows = append(r.rows, user)
	return user, nil
}

func TestGeneratedFixtureRandomAndMemDB(t *testing.T) {
	user := Users.WithName("Ada").Bare()

	assert.Equal(t, user.Name, "Ada")
	assert.Positive(t, user.ID)
	assert.NotEmpty(t, user.Email)

	db := Users.DB(user)
	stored, found := db.Get(user.ID)
	assert.True(t, found, "generated memdb should contain its seed")
	assert.Equal(t, stored, user)
}

func TestGeneratedFixtureCanUseCustomDefault(t *testing.T) {
	user := DefaultUsers.WithName("Ada").Bare()

	assert.Equal(t, user.ID, 41)
	assert.Equal(t, user.Name, "Ada")
	assert.Equal(t, user.Email, "custom@test.dev")
}

func TestGeneratedSeederPersistsBuilderBareValue(t *testing.T) {
	repository := &repository{}
	users := NewUserRepoSeeder(t, repository, context.Background())

	got := users.Persist(Users.WithName("Ada"))

	assert.Equal(t, repository.rows, []domain.User{got})
	assert.Equal(t, got.Name, "Ada")
}

func TestGeneratedDoublesConfigureAndRestoreTogether(t *testing.T) {
	doubles := NewDoubles(t, func(doubles *Doubles) {
		doubles.Mailer.SendFunc = func(context.Context, string) error { return nil }
	})

	for _, name := range []string{"first", "second"} {
		testigo.Run(t, name, func(t *testing.T) {
			ctx := context.Background()
			err := doubles.Mailer.Send(ctx, "ada@test.dev")

			assert.NoError(t, err)
			assert.Expect(t).Called(doubles.Mailer.Send).WithParams(
				ctx,
				"ada@test.dev",
			)
		})
	}
}

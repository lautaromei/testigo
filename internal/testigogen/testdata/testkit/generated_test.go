package testkit

import (
	"context"
	"errors"
	"testing"

	"github.com/lautaromei/testigo"
	"github.com/lautaromei/testigo/assert"
	"github.com/lautaromei/testigo/internal/testigogen/testdata/domain"
)

type repository struct {
	rows []domain.User
}

type mailer struct {
	broadcasts int
	sends      int
}

func (m *mailer) Send(context.Context, string) error                { m.sends++; return nil }
func (m *mailer) Broadcast(context.Context, ...string) (int, error) { return m.broadcasts, nil }
func (*mailer) Ping()                                               {}

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

func TestGeneratedDoublesComposeAndRestoreTogether(t *testing.T) {
	realMailer := &mailer{broadcasts: 7}
	doubles := NewDoubles(t, realMailer, &repository{})

	for _, name := range []string{"first", "second"} {
		testigo.Run(t, name, func(t *testing.T) {
			ctx := context.Background()
			err := doubles.Mailer.Send(ctx, "ada@test.dev")

			assert.NoError(t, err)
			broadcasts, _ := doubles.Mailer.Broadcast(ctx, "ada@test.dev")
			assert.Equal(t, broadcasts, 7)
			assert.Expect(t).Called(doubles.Mailer.Send).WithParams(
				ctx,
				"ada@test.dev",
			)
		})
	}
	assert.Equal(t, realMailer.sends, 2)
}

func TestGeneratedStubsUseFixtureAndError(t *testing.T) {
	stub := NewRepoStub(Users.WithName("Ada"))
	got, err := stub.Upsert(context.Background(), domain.User{})
	assert.NoError(t, err)
	assert.Equal(t, got.Name, "Ada")

	wantErr := errors.New("boom")
	errorStub := NewRepoErrorsErrorStub(Users.WithName("Grace"), wantErr)
	got, err = errorStub.Upsert(context.Background(), domain.User{})
	assert.Equal(t, err, wantErr)
	assert.Equal(t, got.Name, "Grace")
}

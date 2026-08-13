package testigogen

import (
	"bytes"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateMatchesCheckedInContract(t *testing.T) {
	directory := filepath.Join("testdata", "testkit")
	want, err := os.ReadFile(filepath.Join(directory, "zz_testigo_gen.go"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := Generate(Options{Dir: directory, SpecType: "testigoSpec"})
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Fatal("generated source is stale; run go generate ./internal/testigogen/testdata/testkit")
	}
	for _, fragment := range []string{
		"func RandomUser() domain.User",
		"Balance:   int64(random.Int())",
		"func (b usersBuilder) WithEmail",
		"func NewUsersDB",
		"type MailerSpy struct",
		"wrapped *testigo.Reference[domain.Mailer]",
		"return s.wrapped.Get().Broadcast(ctx, to...)",
		"func NewRepoStub(base usersBuilder) *RepoStub",
		"func NewRepoErrorsErrorStub(base usersBuilder, err error) *RepoErrorsErrorStub",
		"func NewUserRepoSeeder",
		"type Doubles struct",
		"func NewDoubles",
	} {
		if !strings.Contains(string(got), fragment) {
			t.Errorf("generated source does not contain %q", fragment)
		}
	}
	for _, forbidden := range []string{"SendFunc", "configure ...func(*MailerSpy)"} {
		if strings.Contains(string(got), forbidden) {
			t.Errorf("generated source must not contain %q", forbidden)
		}
	}
}

func TestValidateStubFixture(t *testing.T) {
	user := types.NewNamed(types.NewTypeName(token.NoPos, nil, "User", nil), types.NewStruct(nil, nil), nil)
	errorType := types.Universe.Lookup("error").Type()

	newInterface := func(results ...types.Type) types.Type {
		vars := make([]*types.Var, len(results))
		for i, result := range results {
			vars[i] = types.NewVar(token.NoPos, nil, "", result)
		}
		method := types.NewFunc(token.NoPos, nil, "Find", types.NewSignatureType(nil, nil, nil, nil, types.NewTuple(vars...), false))
		return types.NewInterfaceType([]*types.Func{method}, nil)
	}

	for _, test := range []struct {
		name    string
		entry   artifact
		iface   types.Type
		wantErr string
	}{
		{name: "stub needs fixture result", entry: artifact{name: "Repo", stub: true, stubFixture: "Users"}, iface: newInterface(errorType), wantErr: "not returned"},
		{name: "error stub needs error result", entry: artifact{name: "Repo", errorStub: true, stubFixture: "Users"}, iface: newInterface(user), wantErr: "must return error"},
		{name: "valid stub", entry: artifact{name: "Repo", stub: true, stubFixture: "Users"}, iface: newInterface(user, errorType)},
		{name: "valid error stub", entry: artifact{name: "Repo", errorStub: true, stubFixture: "Users"}, iface: newInterface(user, errorType)},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.entry.typeOf = test.iface
			err := validateStubFixture(test.entry, user)
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("got %v, want error containing %q", err, test.wantErr)
			}
		})
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	opts := Options{Dir: filepath.Join("testdata", "testkit"), SpecType: "testigoSpec"}
	first, err := Generate(opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(opts)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Fatal("two generations from the same package differ")
	}
}

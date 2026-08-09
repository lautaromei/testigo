package testigogen

import (
	"bytes"
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
		"func NewUserRepoSeeder",
		"type Doubles struct",
		"func NewDoubles",
	} {
		if !strings.Contains(string(got), fragment) {
			t.Errorf("generated source does not contain %q", fragment)
		}
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

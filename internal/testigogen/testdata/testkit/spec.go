//go:generate go run ../../../../cmd/testigo-gen

package testkit

import "github.com/lautaromei/testigo/internal/testigogen/testdata/domain"

func defaultUser() domain.User {
	return domain.User{ID: 41, Name: "Custom default", Email: "custom@test.dev"}
}

type testigoSpec struct {
	Users        domain.User           `testigo:"fixture,memdb=ID"`
	DefaultUsers domain.User           `testigo:"fixture,default=defaultUser"`
	Mailer       domain.Mailer         `testigo:"spy"`
	UserRepo     domain.UserRepository `testigo:"spy,seeder=Upsert"`
	Repo         domain.UserRepository `testigo:"stub=Users"`
	RepoErrors   domain.UserRepository `testigo:"errorstub=Users"`
}

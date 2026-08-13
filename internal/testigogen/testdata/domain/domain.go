package domain

import (
	"context"
	"time"
)

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

type User struct {
	ID        int
	Balance   int64
	Name      string
	Email     string
	Active    bool
	Role      Role
	CreatedAt time.Time
	Tags      []string
	Secret    string `testigo:"-"`
}

type Mailer interface {
	Send(ctx context.Context, to string) error
	Broadcast(ctx context.Context, to ...string) (int, error)
	Ping()
}

type UserRepository interface {
	Upsert(ctx context.Context, user User) (User, error)
}

type UserCatalog interface {
	List(ctx context.Context) ([]User, error)
	Get(ctx context.Context, id int) (User, bool, error)
}

type UserWriter interface {
	Save(ctx context.Context, user User) error
}

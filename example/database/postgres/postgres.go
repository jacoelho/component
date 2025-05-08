package postgres

import (
	"context"
	"fmt"

	"github.com/jacoelho/component"
	"github.com/jacoelho/component/example/database"
)

type Postgres struct{}

func (*Postgres) Start(context.Context) error {
	fmt.Println("starting postgres")
	return nil
}

func (*Postgres) Stop(context.Context) error {
	fmt.Println("stopping postgres")
	return nil
}

func (*Postgres) GetByID(context.Context, string) (string, error) {
	return "postgres", nil
}

func Provide(_ *component.System) (database.Database, error) {
	return &Postgres{}, nil
}

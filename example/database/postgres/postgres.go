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

func (*Postgres) GetByID(_ context.Context, id string) (string, error) {
	return id, nil
}

func Provide(_ *component.System) (database.Database, error) {
	return &Postgres{}, nil
}

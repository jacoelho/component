package mysql

import (
	"context"
	"fmt"

	"github.com/jacoelho/component"
	"github.com/jacoelho/component/example/database"
)

type MySQL struct{}

func (*MySQL) Start(context.Context) error {
	fmt.Println("starting mysql")
	return nil
}

func (*MySQL) Stop(context.Context) error {
	fmt.Println("stopping mysql")
	return nil
}

func (*MySQL) GetByID(_ context.Context, id string) (string, error) {
	return id, nil
}

func Provide(_ *component.System) (database.Database, error) {
	return &MySQL{}, nil
}

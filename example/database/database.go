package database

import (
	"context"

	"github.com/jacoelho/component"
)

var DatabaseKey component.Key[Database]

type Database interface {
	Start(context.Context) error
	Stop(context.Context) error
	GetByID(context.Context, string) (string, error)
}

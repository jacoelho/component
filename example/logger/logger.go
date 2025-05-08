package logger

import (
	"context"
	"fmt"

	"github.com/jacoelho/component"
)

var LoggerKey component.Key[*Logger]

type Logger struct{}

func (l *Logger) Start(ctx context.Context) error {
	fmt.Println("Starting logger")
	return nil
}

func (l *Logger) Stop(ctx context.Context) error {
	fmt.Println("Stopping logger")
	return nil
}

func (l *Logger) Log(message string) {
	fmt.Println("Log message: ", message)
}

func Provide(_ *component.System) (*Logger, error) {
	return &Logger{}, nil
}

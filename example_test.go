package component_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jacoelho/component"
)

type exampleDatabase struct {
	events *[]string
}

type exampleService struct {
	database *exampleDatabase
}

func (database *exampleDatabase) Start(context.Context) error {
	*database.events = append(*database.events, "start database")
	return nil
}

func (database *exampleDatabase) Stop(context.Context) error {
	*database.events = append(*database.events, "stop database")
	return nil
}

func Example() {
	events := []string{}
	database := component.Provide(
		func() *exampleDatabase {
			return &exampleDatabase{events: &events}
		},
		component.Managed[*exampleDatabase](),
	)
	service := database.Map(func(database *exampleDatabase) *exampleService {
		*database.events = append(*database.events, "construct service")
		return &exampleService{database: database}
	})

	runtime, err := component.New(service)
	if err != nil {
		panic(err)
	}
	startCtx, cancelStart := context.WithTimeout(context.Background(), time.Second)
	defer cancelStart()
	if err := runtime.Start(startCtx); err != nil {
		panic(err)
	}
	stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Second)
	defer cancelStop()
	if err := runtime.Stop(stopCtx); err != nil {
		panic(err)
	}

	fmt.Println(strings.Join(events, ","))
	// Output: start database,construct service,stop database
}

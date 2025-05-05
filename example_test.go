package component_test

import (
	"context"
	"fmt"
	"time"

	"github.com/jacoelho/component"
)

type Database struct{ DSN string }

func (db *Database) Start(ctx context.Context) error {
	time.Sleep(time.Millisecond * 100)
	fmt.Println("↳ DB connect:", db.DSN)
	return nil
}

func (db *Database) Stop(ctx context.Context) error {
	time.Sleep(time.Millisecond * 100)
	fmt.Println("↳ DB close")
	return nil
}

type MessageQueue struct{ URL string }

func (mq *MessageQueue) Start(ctx context.Context) error {
	time.Sleep(time.Millisecond * 200)
	fmt.Println("↳ MQ connect:", mq.URL)
	return nil
}

func (mq *MessageQueue) Stop(ctx context.Context) error {
	time.Sleep(time.Millisecond * 200)
	fmt.Println("↳ MQ close")
	return nil
}

type AppService struct {
	DB *Database
	MQ *MessageQueue
}

func (a *AppService) Start(ctx context.Context) error {
	fmt.Println("↳ AppService ready with DB & MQ")
	return nil
}

func (a *AppService) Stop(ctx context.Context) error {
	fmt.Println("↳ AppService stopping")
	return nil
}

func Example() {
	ctx := context.Background()
	sys := new(component.System)

	var (
		DBKey  component.Key[*Database]
		MQKey  component.Key[*MessageQueue]
		AppKey component.Key[*AppService]
	)

	// Provide components and wire walkDependencies
	component.Provide(sys, DBKey, func(sys *component.System) (*Database, error) {
		return &Database{DSN: "postgres://..."}, nil
	})
	component.Provide(sys, MQKey, func(_ *component.System) (*MessageQueue, error) {
		return &MessageQueue{URL: "amqp://..."}, nil
	})
	component.Provide(sys, AppKey, func(s *component.System) (*AppService, error) {
		db, err := component.Get(s, DBKey)
		if err != nil {
			return nil, err
		}
		mq, err := component.Get(s, MQKey)
		if err != nil {
			return nil, err
		}
		return &AppService{DB: db, MQ: mq}, nil
	}, DBKey, MQKey)

	// Start all components (parallel within each dependency-level)
	if err := sys.Start(ctx); err != nil {
		panic(err)
	}

	fmt.Println("▶ system is UP")

	// Stop all components in reverse order (parallel within each dependency-level)
	if err := sys.Stop(ctx); err != nil {
		return
	}

	// Output:
	//↳ DB connect: postgres://...
	//↳ MQ connect: amqp://...
	//↳ AppService ready with DB & MQ
	//▶ system is UP
	//↳ AppService stopping
	//↳ DB close
	//↳ MQ close
}

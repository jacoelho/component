package component_test

import (
	"context"
	"fmt"
	"time"

	"github.com/jacoelho/component"
)

type Database struct{ DSN string }

const (
	dbStartDelay  = 250 * time.Millisecond
	dbStopDelay   = 120 * time.Millisecond
	mqStartDelay  = 450 * time.Millisecond
	mqStopDelay   = 180 * time.Millisecond
	appStartDelay = 80 * time.Millisecond
	appStopDelay  = 60 * time.Millisecond
)

func waitLatency(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (db *Database) Start(ctx context.Context) error {
	if err := waitLatency(ctx, dbStartDelay); err != nil {
		return err
	}
	fmt.Println("↳ DB connect:", db.DSN)
	return nil
}

func (db *Database) Stop(ctx context.Context) error {
	if err := waitLatency(ctx, dbStopDelay); err != nil {
		return err
	}
	fmt.Println("↳ DB close")
	return nil
}

type MessageQueue struct{ URL string }

func (mq *MessageQueue) Start(ctx context.Context) error {
	if err := waitLatency(ctx, mqStartDelay); err != nil {
		return err
	}
	fmt.Println("↳ MQ connect:", mq.URL)
	return nil
}

func (mq *MessageQueue) Stop(ctx context.Context) error {
	if err := waitLatency(ctx, mqStopDelay); err != nil {
		return err
	}
	fmt.Println("↳ MQ close")
	return nil
}

type AppService struct {
	DB *Database
	MQ *MessageQueue
}

func (a *AppService) Start(ctx context.Context) error {
	if err := waitLatency(ctx, appStartDelay); err != nil {
		return err
	}
	fmt.Println("↳ AppService ready with DB & MQ")
	return nil
}

func (a *AppService) Stop(ctx context.Context) error {
	if err := waitLatency(ctx, appStopDelay); err != nil {
		return err
	}
	fmt.Println("↳ AppService stopping")
	return nil
}

func Example() {
	ctx := context.Background()
	reg := component.NewRegistry()

	var (
		dbKey  = component.NewKey[*Database]("db")
		mqKey  = component.NewKey[*MessageQueue]("mq")
		appKey = component.NewKey[*AppService]("app")
	)

	// Provide components and declare dependencies.
	_ = component.Provide(reg, dbKey, func(rt *component.Runtime) (*Database, error) {
		return &Database{DSN: "postgres://..."}, nil
	})
	_ = component.Provide(reg, mqKey, func(_ *component.Runtime) (*MessageQueue, error) {
		return &MessageQueue{URL: "amqp://..."}, nil
	})
	_ = component.Provide(reg, appKey, func(rt *component.Runtime) (*AppService, error) {
		db, err := component.Get(rt, dbKey)
		if err != nil {
			return nil, err
		}
		mq, err := component.Get(rt, mqKey)
		if err != nil {
			return nil, err
		}
		return &AppService{DB: db, MQ: mq}, nil
	}, dbKey, mqKey)

	plan, err := reg.Compile()
	if err != nil {
		panic(err)
	}

	rt, err := plan.NewRuntime()
	if err != nil {
		panic(err)
	}

	if err := rt.Start(ctx); err != nil {
		panic(err)
	}

	fmt.Println("▶ runtime is UP")

	if err := rt.Stop(ctx); err != nil {
		return
	}

	// Output:
	//↳ DB connect: postgres://...
	//↳ MQ connect: amqp://...
	//↳ AppService ready with DB & MQ
	//▶ runtime is UP
	//↳ AppService stopping
	//↳ DB close
	//↳ MQ close
}

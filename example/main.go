package main

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/jacoelho/component"
	"github.com/jacoelho/component/example/database"
	"github.com/jacoelho/component/example/database/mysql"
	"github.com/jacoelho/component/example/logger"
)

// 1. Define components

type MainService struct {
	logger         *logger.Logger
	db             database.Database
	isShuttingDown atomic.Bool
}

func (s *MainService) Start(ctx context.Context) error {
	s.logger.Log("starting MainService")
	return nil
}

func (s *MainService) Stop(ctx context.Context) error {
	s.logger.Log("stopping MainService")
	s.isShuttingDown.Store(true)
	time.Sleep(5 * time.Second)
	// shutdown http server for example
	return nil
}

func main() {
	ctx := context.Background()
	reg := component.NewRegistry()

	if err := component.Provide(reg, logger.LoggerKey, logger.Provide); err != nil {
		log.Fatalf("Failed to provide logger: %v", err)
	}

	if err := component.Provide(reg, database.DatabaseKey, mysql.Provide); err != nil {
		log.Fatalf("Failed to provide database: %v", err)
	}

	mainServiceKey := component.NewKey[*MainService]("main")
	if err := component.Provide(reg, mainServiceKey, func(rt *component.Runtime) (*MainService, error) {
		logComp, err := component.Get(rt, logger.LoggerKey)
		if err != nil {
			return nil, err
		}

		db, err := component.Get(rt, database.DatabaseKey)
		if err != nil {
			return nil, err
		}

		svc := &MainService{
			logger: logComp,
			db:     db,
		}
		return svc, nil
	}, logger.LoggerKey, database.DatabaseKey); err != nil {
		log.Fatalf("Failed to provide main service: %v", err)
	}

	plan, err := reg.Compile()
	if err != nil {
		log.Fatalf("Failed to compile plan: %v", err)
	}

	rt := plan.NewRuntime()

	fmt.Println("Starting runtime...")

	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := rt.Start(startCtx); err != nil {
		log.Fatalf("Runtime start failed: %v", err)
	}

	fmt.Println("Runtime is UP.")

	fmt.Println("Stopping runtime...")

	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := rt.Stop(stopCtx); err != nil {
		log.Printf("Runtime stop encountered errors: %v", err)
	}
	fmt.Println("Runtime shut down.")
}

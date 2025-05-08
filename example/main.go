package main

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/jacoelho/component/example/database"
	"github.com/jacoelho/component/example/database/mysql"
	"github.com/jacoelho/component/example/logger"

	"github.com/jacoelho/component"
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
	sys := new(component.System)
	ctx := context.Background()

	if err := component.Provide(sys, logger.LoggerKey, logger.Provide); err != nil {
		log.Fatalf("Failed to provide logger: %v", err)
	}

	if err := component.Provide(sys, database.DatabaseKey, mysql.Provide); err != nil {
		log.Fatalf("Failed to provide database: %v", err)
	}

	if err := component.ProvideWithoutKey(sys, func(s *component.System) (*MainService, error) {
		log, err := component.Get(s, logger.LoggerKey)
		if err != nil {
			return nil, err
		}

		db, err := component.Get(s, database.DatabaseKey)
		if err != nil {
			return nil, err
		}

		svc := &MainService{
			logger: log,
			db:     db,
		}
		return svc, nil
	}, logger.LoggerKey, database.DatabaseKey); err != nil {
		log.Fatalf("Failed to provide main service: %v", err)
	}

	fmt.Println("Starting system...")

	startCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	if err := sys.Start(startCtx); err != nil {
		log.Fatalf("System start failed: %v", err)
	}

	fmt.Println("System is UP.")

	fmt.Println("Stopping system...")

	stopCtx, cancel := context.WithTimeout(ctx, time.Second*10)

	defer cancel()
	if err := sys.Stop(stopCtx); err != nil {
		log.Printf("System stop encountered errors: %v", err)
	}
	fmt.Println("System shut down.")
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Sockar/t4c-like-server/internal/game"
	"github.com/Sockar/t4c-like-server/internal/network"
	"github.com/Sockar/t4c-like-server/internal/persistence"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	address := flag.String("addr", ":8080", "HTTP/WebSocket listen address")
	databasePath := flag.String("db", "./data/world.db", "SQLite database file")
	flag.Parse()

	if directory := filepath.Dir(*databasePath); directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create database directory: %w", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := persistence.Open(ctx, *databasePath)
	if err != nil {
		return err
	}
	defer store.Close()

	world := game.NewWorld(store, 100*time.Millisecond)
	if err := world.Validate(); err != nil {
		return err
	}
	worldDone := make(chan struct{})
	go func() {
		defer close(worldDone)
		world.Run(ctx)
	}()

	websocketServer := network.NewServer(world)
	server := &http.Server{
		Addr:              *address,
		Handler:           websocketServer,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-serverDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			stop()
			<-worldDone
			return fmt.Errorf("serve HTTP: %w", err)
		}
	}

	websocketServer.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		websocketServer.Close()
		_ = server.Close()
		stop()
		<-worldDone
		return fmt.Errorf("shut down HTTP server: %w", err)
	}
	stop()
	<-worldDone
	return nil
}

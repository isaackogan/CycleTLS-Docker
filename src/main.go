package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/Danny-Dasilva/CycleTLS/cycletls"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	port := os.Getenv("WS_PORT")
	if port == "" {
		port = "9112"
	}
	addr := ":" + port

	mux := http.NewServeMux()
	mux.HandleFunc("/health_check", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", cycletls.WSEndpoint)

	srv := &http.Server{Addr: addr, Handler: mux}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("CycleTLS WebSocket server listening on %s", addr)
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		// ListenAndServe returned on its own. The listener is dead, so the
		// process must exit so the orchestrator can restart the container.
		log.Fatalf("cycletls server exited unexpectedly: %v", err)
	case <-ctx.Done():
		log.Println("shutdown signal received, stopping server")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("graceful shutdown failed: %v", err)
		os.Exit(1)
	}
}

package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/alaexeyshustov/email-fetcher/internal/config"
)

func main() {
	cfg := config.Load()

	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// grpc.NewServer() and EmailServiceServer registration wired here
	// once proto/email/v1 code generation is complete.

	log.Printf("email-fetcher listening on %s (env=%s log=%s)", lis.Addr(), cfg.Env, cfg.LogLevel)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("email-fetcher shutting down (timeout=%s)", cfg.ShutdownTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	// srv.GracefulStop() blocks until active RPCs complete or ctx expires,
	// preventing indefinite hangs during rolling deploys.
	_ = ctx // removed once grpc.Server is wired

	log.Println("email-fetcher stopped")
}

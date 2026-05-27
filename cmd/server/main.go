package main

import (
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

	log.Printf("email-fetcher listening on %s (env=%s log=%s)", lis.Addr(), cfg.Env, cfg.LogLevel)

	// gRPC server registration is wired here once proto generation is complete.

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("email-fetcher shutting down")
}

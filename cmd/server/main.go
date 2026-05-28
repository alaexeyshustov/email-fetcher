package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	emailv1 "github.com/alaexeyshustov/email-fetcher/gen/go/email/v1"
	"github.com/alaexeyshustov/email-fetcher/internal/config"
	"github.com/alaexeyshustov/email-fetcher/internal/fanout"
	providerPkg "github.com/alaexeyshustov/email-fetcher/internal/provider"
	"github.com/alaexeyshustov/email-fetcher/internal/provider/gmail"
	"github.com/alaexeyshustov/email-fetcher/internal/provider/yahoo"
	"github.com/alaexeyshustov/email-fetcher/internal/server"
)

func main() {
	cfg := config.Load()

	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	providers := map[emailv1.Provider]providerPkg.Provider{
		emailv1.Provider_GMAIL: gmail.New(),
		emailv1.Provider_YAHOO: yahoo.New(),
	}

	srv := grpc.NewServer()
	emailv1.RegisterEmailServiceServer(srv, server.New(fanout.New(providers)))

	log.Printf("email-fetcher listening on %s (env=%s log=%s)", lis.Addr(), cfg.Env, cfg.LogLevel)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Printf("grpc serve: %v", err)
		}
	}()

	<-quit
	log.Printf("email-fetcher shutting down (timeout=%s)", cfg.ShutdownTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-ctx.Done():
		srv.Stop()
	}

	log.Println("email-fetcher stopped")
}

package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
)

func TestServerStartReturnsListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{httpServer: &http.Server{Addr: ln.Addr().String()}}
	if err := s.Start(ctx); err == nil {
		t.Fatal("Start succeeded on an occupied port")
	}
}

func TestServerStartRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &Server{httpServer: &http.Server{Addr: "127.0.0.1:0"}}
	if err := s.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v, want context.Canceled", err)
	}
}

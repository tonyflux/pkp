package netproxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRaceNodesFallsBackAndClosesLosingConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var loserClosed atomic.Bool
	late := make(chan struct{})
	slowStarted := make(chan struct{})
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == "dead" {
			return nil, errors.New("refused")
		}
		client, server := net.Pipe()
		if address == "slow" {
			close(slowStarted)
			<-ctx.Done()
			close(late)
			go func() {
				defer server.Close()
				var b [1]byte
				_, err := server.Read(b[:])
				loserClosed.Store(err != nil)
			}()
			return client, nil
		}
		<-slowStarted
		go func() { defer server.Close(); var b [1]byte; server.Read(b[:]) }()
		return client, nil
	}
	conn, best, err := raceNodes(ctx, "tcp", []string{"dead", "slow", "fast"}, dial)
	if err != nil || best != "fast" {
		t.Fatalf("selected %q: %v", best, err)
	}
	conn.Close()
	<-late
	deadline := time.Now().Add(time.Second)
	for !loserClosed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !loserClosed.Load() {
		t.Fatal("losing connection leaked")
	}
}

func TestRaceNodesDoesNotDiscardLaterAddresses(t *testing.T) {
	addresses := []string{"1", "2", "3", "4", "5", "6", "7", "8", "good"}
	client, server := net.Pipe()
	defer server.Close()
	conn, best, err := raceNodes(context.Background(), "tcp", addresses, func(_ context.Context, _, address string) (net.Conn, error) {
		if address == "good" {
			return client, nil
		}
		return nil, errors.New("refused")
	})
	if err != nil || best != "good" {
		t.Fatalf("last node not tried: %q %v", best, err)
	}
	conn.Close()
}

func TestMeasurePreservesHTTPStatusAndRejectsInvalidTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := Measure(ctx, Config{Mode: ModeDirect}, server.URL)
	if result.Reachable {
		t.Fatal("untrusted TLS was accepted")
	}
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "HEAD" {
			t.Errorf("unexpected method %s", r.Method)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected credentials")
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer plain.Close()
	result = Measure(ctx, Config{Mode: ModeDirect}, plain.URL)
	if !result.Reachable || result.Status != 401 || result.Node == "" {
		t.Fatalf("reachable API rejection lost: %+v", result)
	}
}

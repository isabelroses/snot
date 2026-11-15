package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// crawler answers requestCrawl with the scripted statuses, repeating the last.
type crawler struct {
	*httptest.Server
	hits   atomic.Int32
	mu     sync.Mutex
	bodies []string
}

func newCrawler(t *testing.T, statuses ...int) *crawler {
	t.Helper()
	c := &crawler{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /xrpc/com.atproto.sync.requestCrawl", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies = append(c.bodies, string(body))
		c.mu.Unlock()
		hit := int(c.hits.Add(1)) - 1
		w.WriteHeader(statuses[min(hit, len(statuses)-1)])
	})
	c.Server = httptest.NewServer(mux)
	t.Cleanup(c.Close)
	return c
}

func (c *crawler) awaitHits(t *testing.T, n int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.hits.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("crawler saw %d requests, want %d", c.hits.Load(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRequestCrawlsWaitsForListenerThenRetriesUntilAcceptedOrRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	scripted := newCrawler(t, http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusOK)
	down := newCrawler(t, http.StatusServiceUnavailable)
	rejecting := newCrawler(t, http.StatusNotFound)

	// reserve a port, then free it so nothing is listening yet
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := reserved.Addr().String()
	reserved.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		requestCrawls(ctx, addr, []string{scripted.URL, down.URL + "/", rejecting.URL}, "knot.test", 20*time.Millisecond, logger)
	}()

	time.Sleep(150 * time.Millisecond)
	for name, c := range map[string]*crawler{"scripted": scripted, "down": down, "rejecting": rejecting} {
		if n := c.hits.Load(); n != 0 {
			t.Fatalf("%s crawler got %d requests before the listener was up", name, n)
		}
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	<-done

	scripted.awaitHits(t, 3)
	rejecting.awaitHits(t, 1)
	down.awaitHits(t, 2)

	time.Sleep(100 * time.Millisecond)
	if n := scripted.hits.Load(); n != 3 {
		t.Errorf("accepted crawler was asked %d times, want 3", n)
	}
	if n := rejecting.hits.Load(); n != 1 {
		t.Errorf("rejecting crawler was asked %d times, want 1", n)
	}
	scripted.mu.Lock()
	for _, body := range scripted.bodies {
		if body != `{"hostname":"knot.test"}` {
			t.Errorf("requestCrawl body = %s", body)
		}
	}
	scripted.mu.Unlock()

	cancel()
	time.Sleep(100 * time.Millisecond)
	frozen := down.hits.Load()
	time.Sleep(100 * time.Millisecond)
	if n := down.hits.Load(); n != frozen {
		t.Errorf("retries continued after shutdown: %d then %d", frozen, n)
	}
}

func TestProbeAddr(t *testing.T) {
	for listen, want := range map[string]string{
		":5555":          "localhost:5555",
		"0.0.0.0:5555":   "127.0.0.1:5555",
		"[::]:5555":      "[::1]:5555",
		"127.0.0.1:5555": "127.0.0.1:5555",
		"10.0.0.4:5555":  "10.0.0.4:5555",
	} {
		if got := probeAddr(listen); got != want {
			t.Errorf("probeAddr(%q) = %q, want %q", listen, got, want)
		}
	}
}

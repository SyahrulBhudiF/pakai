package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func testPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func writeHealth(t *testing.T, w http.ResponseWriter, providers []string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"status": "ok", "providers": providers}); err != nil {
		t.Error(err)
	}
}

// /health is live before the refresh loop has produced a snapshot, so an empty
// /status must not be accepted as ready.
func TestEnsureRunningWaitsForNonEmptyStatus(t *testing.T) {
	var statusCalls int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			writeHealth(t, w, []string{"claude"})
		case "/status":
			w.Header().Set("Content-Type", "application/json")
			if atomic.AddInt32(&statusCalls, 1) == 1 {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"provider":"claude","label":"claude","status":"ok"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := EnsureRunning(testPort(t, server)); err != nil {
		t.Fatalf("EnsureRunning returned error: %v", err)
	}
	if got := atomic.LoadInt32(&statusCalls); got < 2 {
		t.Fatalf("/status polled %d time(s), want at least 2: an empty status was accepted as ready", got)
	}
}

// A daemon with nothing to poll stays empty forever, so the caller must not be
// stalled until the deadline.
func TestEnsureRunningReturnsWhenDaemonHasNoProviders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			writeHealth(t, w, nil)
		case "/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	start := time.Now()
	if err := EnsureRunning(testPort(t, server)); err != nil {
		t.Fatalf("EnsureRunning returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("EnsureRunning waited %v for a daemon with no providers", elapsed)
	}
}

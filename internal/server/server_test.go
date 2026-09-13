package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadinessFlag(t *testing.T) {
	s := New(Config{FailureThreshold: 3})
	if s.Ready() {
		t.Fatal("should start not ready")
	}
	s.SetReady(true)
	if !s.Ready() {
		t.Fatal("should be ready after SetReady(true)")
	}
	s.SetReady(false)
	if s.Ready() {
		t.Fatal("should be not ready after SetReady(false)")
	}
}

func TestMarkSuccessFlipsReady(t *testing.T) {
	s := New(Config{FailureThreshold: 3})
	if s.Ready() {
		t.Fatal("should start not ready")
	}
	s.MarkSuccess()
	if !s.Ready() {
		t.Fatal("should be ready after MarkSuccess")
	}
}

func TestMarkFailureThresholdFlipsNotReady(t *testing.T) {
	s := New(Config{FailureThreshold: 3})
	s.MarkSuccess()

	// Failures below the threshold hold the last-good ready state.
	s.MarkFailure()
	s.MarkFailure()
	if !s.Ready() {
		t.Fatalf("should stay ready with 2 < 3 consecutive failures")
	}
	// The third consecutive failure crosses the threshold.
	s.MarkFailure()
	if s.Ready() {
		t.Fatalf("should be not ready after 3 consecutive failures")
	}

	// A success resets the counter, so the threshold restarts from zero.
	s.MarkSuccess()
	if !s.Ready() {
		t.Fatalf("should be ready again after MarkSuccess")
	}
	s.MarkFailure()
	s.MarkFailure()
	if !s.Ready() {
		t.Fatalf("counter should have reset on success; 2 < 3 must stay ready")
	}
}

func TestThresholdBelowOneTreatedAsOne(t *testing.T) {
	s := New(Config{FailureThreshold: 0})
	s.MarkSuccess()
	s.MarkFailure()
	if s.Ready() {
		t.Fatalf("threshold clamped to 1: a single failure must flip not-ready")
	}
}

func TestProbes(t *testing.T) {
	s := New(Config{FailureThreshold: 1})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz = %v / %v", resp, err)
	}
	resp, _ = http.Get(srv.URL + "/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readyz before ready = %d, want 503", resp.StatusCode)
	}
	s.SetReady(true)
	resp, _ = http.Get(srv.URL + "/readyz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz after SetReady = %d, want 200", resp.StatusCode)
	}
}

func TestRootStatusPage(t *testing.T) {
	s := New(Config{FailureThreshold: 1})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET / error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), serviceName) {
		t.Errorf("page missing service name:\n%s", body)
	}
}

func TestUnknownPath404(t *testing.T) {
	s := New(Config{FailureThreshold: 1})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatalf("GET /nope error: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /nope = %d, want 404", resp.StatusCode)
	}
}

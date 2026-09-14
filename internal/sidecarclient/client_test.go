package sidecarclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/sidecarclient"
)

// fakeSidecar stands up a contract-shaped sidecar and records the last request
// the client sent, so tests can assert both directions of the wire.
type fakeSidecar struct {
	server     *httptest.Server
	lastMethod string
	lastPath   string
	lastBody   map[string]any
}

func newFakeSidecar(t *testing.T, handler http.HandlerFunc) *fakeSidecar {
	t.Helper()
	f := &fakeSidecar{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.lastMethod = r.Method
		f.lastPath = r.URL.Path
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&f.lastBody)
		}
		handler(w, r)
	}))
	t.Cleanup(f.server.Close)
	return f
}

// writeEnvelope writes a {error:{code,message}} envelope with the given status.
func writeEnvelope(t *testing.T, w http.ResponseWriter, status int, code, msg string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	}); err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}

func TestReadInput(t *testing.T) {
	want := []uint16{26, 9, 8, 15, 38, 59}
	fake := newFakeSidecar(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, 200, map[string]any{"addr": 33022, "count": 6, "regs": want})
	})
	client := sidecarclient.New(fake.server.URL)

	got, err := client.ReadInput(t.Context(), 33022, 6)
	if err != nil {
		t.Fatalf("ReadInput: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("regs = %v, want %v", got, want)
	}
	if fake.lastMethod != http.MethodPost || fake.lastPath != "/read_input" {
		t.Fatalf("request = %s %s, want POST /read_input", fake.lastMethod, fake.lastPath)
	}
	if fake.lastBody["addr"] != float64(33022) || fake.lastBody["count"] != float64(6) {
		t.Fatalf("request body = %v, want addr=33022 count=6", fake.lastBody)
	}
}

func TestReadHolding(t *testing.T) {
	want := []uint16{340, 340}
	fake := newFakeSidecar(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, 200, map[string]any{"addr": 43141, "count": 2, "regs": want})
	})
	client := sidecarclient.New(fake.server.URL)

	got, err := client.ReadHolding(t.Context(), 43141, 2)
	if err != nil {
		t.Fatalf("ReadHolding: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("regs = %v, want %v", got, want)
	}
	if fake.lastPath != "/read_holding" {
		t.Fatalf("path = %s, want /read_holding", fake.lastPath)
	}
}

func TestWriteHolding(t *testing.T) {
	fake := newFakeSidecar(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, 200, map[string]any{"addr": 43141, "value": 340, "ok": true})
	})
	client := sidecarclient.New(fake.server.URL)

	if err := client.WriteHolding(t.Context(), 43141, 340); err != nil {
		t.Fatalf("WriteHolding: %v", err)
	}
	if fake.lastMethod != http.MethodPost || fake.lastPath != "/write_holding" {
		t.Fatalf("request = %s %s, want POST /write_holding", fake.lastMethod, fake.lastPath)
	}
	if fake.lastBody["addr"] != float64(43141) || fake.lastBody["value"] != float64(340) {
		t.Fatalf("request body = %v, want addr=43141 value=340", fake.lastBody)
	}
}

func TestWriteHoldingOKFalse(t *testing.T) {
	fake := newFakeSidecar(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, 200, map[string]any{"addr": 43141, "value": 340, "ok": false})
	})
	client := sidecarclient.New(fake.server.URL)

	if err := client.WriteHolding(t.Context(), 43141, 340); err == nil {
		t.Fatal("WriteHolding: want error on ok=false, got nil")
	}
}

func TestHealth(t *testing.T) {
	fake := newFakeSidecar(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, 200, map[string]any{"ok": true, "inverter_reachable": true, "mode": "mock"})
	})
	client := sidecarclient.New(fake.server.URL)

	got, err := client.Health(t.Context())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	want := sidecarclient.Health{OK: true, InverterReachable: true, Mode: "mock"}
	if got != want {
		t.Fatalf("health = %+v, want %+v", got, want)
	}
	if fake.lastMethod != http.MethodGet || fake.lastPath != "/health" {
		t.Fatalf("request = %s %s, want GET /health", fake.lastMethod, fake.lastPath)
	}
}

func TestErrorEnvelopeMapsToSentinel(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		want   error
	}{
		{"bad_request", 400, "bad_request", sidecarclient.ErrBadRequest},
		{"timeout", 504, "timeout", sidecarclient.ErrTimeout},
		{"illegal_address", 502, "illegal_address", sidecarclient.ErrIllegalAddress},
		{"frame_error", 502, "frame_error", sidecarclient.ErrFrame},
		{"connection_error", 503, "connection_error", sidecarclient.ErrConnection},
		{"not_found", 404, "not_found", sidecarclient.ErrNotFound},
		{"internal_error", 500, "internal_error", sidecarclient.ErrInternal},
		{"unknown_code", 500, "some_new_code", sidecarclient.ErrInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeSidecar(t, func(w http.ResponseWriter, _ *http.Request) {
				writeEnvelope(t, w, tc.status, tc.code, "boom")
			})
			client := sidecarclient.New(fake.server.URL)

			_, err := client.ReadInput(t.Context(), 33022, 6)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want errors.Is %v", err, tc.want)
			}

			var se *sidecarclient.SidecarError
			if !errors.As(err, &se) {
				t.Fatalf("err = %v, want a *SidecarError in the chain", err)
			}
			if se.Code != tc.code || se.Status != tc.status || se.Message != "boom" {
				t.Fatalf("SidecarError = %+v, want code=%s status=%d message=boom", se, tc.code, tc.status)
			}
		})
	}
}

func TestNonEnvelopeErrorIsGeneric(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty_body", ""},
		{"html_body", "<html>502 Bad Gateway</html>"},
		{"envelope_without_code", `{"error":{"message":"no code here"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeSidecar(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(502)
				_, _ = w.Write([]byte(tc.body))
			})
			client := sidecarclient.New(fake.server.URL)

			_, err := client.ReadInput(t.Context(), 33022, 6)
			if err == nil {
				t.Fatal("want a non-nil error, got nil")
			}

			var se *sidecarclient.SidecarError
			if errors.As(err, &se) {
				t.Fatalf("want a generic error, got a *SidecarError: %v", err)
			}
		})
	}
}

func TestContextCancellation(t *testing.T) {
	fake := newFakeSidecar(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, 200, map[string]any{"addr": 33022, "count": 6, "regs": []uint16{}})
	})
	client := sidecarclient.New(fake.server.URL)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := client.ReadInput(ctx, 33022, 6); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestComposesWithInverterDecode proves the two packages compose: a raw
// ReadInput result feeds directly into inverter.Block for decoding. The client
// does not import inverter; only this test does, keeping the dependency direction
// clean.
func TestComposesWithInverterDecode(t *testing.T) {
	const base = 33022
	fake := newFakeSidecar(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, 200, map[string]any{"addr": base, "count": 3, "regs": []uint16{26, 9, 8}})
	})
	client := sidecarclient.New(fake.server.URL)

	regs, err := client.ReadInput(t.Context(), base, 3)
	if err != nil {
		t.Fatalf("ReadInput: %v", err)
	}

	block := inverter.Block{Base: base, Regs: regs}
	got, err := block.U16(base + 1)
	if err != nil {
		t.Fatalf("block.U16: %v", err)
	}
	if got != 9 {
		t.Fatalf("U16 = %d, want 9", got)
	}
}

func TestWaitUntilServingWaitsForTheListener(t *testing.T) {
	const failures = 3
	var calls atomic.Int32
	fake := newFakeSidecar(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= failures {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSON(t, w, 200, map[string]any{"ok": true, "inverter_reachable": true, "mode": "mock"})
	})
	client := sidecarclient.New(fake.server.URL)

	if err := client.WaitUntilServing(t.Context(), time.Millisecond); err != nil {
		t.Fatalf("WaitUntilServing: %v", err)
	}
	if got := calls.Load(); got != failures+1 {
		t.Fatalf("calls = %d, want %d", got, failures+1)
	}
}

func TestWaitUntilServingGivesUpWhenTheContextEnds(t *testing.T) {
	fake := newFakeSidecar(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	client := sidecarclient.New(fake.server.URL)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	err := client.WaitUntilServing(ctx, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

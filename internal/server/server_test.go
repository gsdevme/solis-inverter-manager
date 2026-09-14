package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	resp, body := getRootResponse(t, s)

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(body, serviceName) {
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

func TestRootStatusPageNoValuesYet(t *testing.T) {
	s := New(Config{FailureThreshold: 1})
	body := getRoot(t, s)

	if !strings.Contains(body, "no readings yet") {
		t.Errorf("page before the first reading should say so:\n%s", body)
	}
	if strings.Contains(body, "<table") {
		t.Errorf("page before the first reading should not render a table:\n%s", body)
	}
}

func TestRootStatusPageRendersRecordedValues(t *testing.T) {
	s := New(Config{FailureThreshold: 1})
	recordReading(t, s, `{"grid_power_w":-1234,"battery_soc":57,"set_charge_current":2.5,"boost_ends_at":null}`)
	body := getRoot(t, s)

	for _, want := range []string{"battery_soc", "57", "grid_power_w", "-1234", "set_charge_current", "2.5", "boost_ends_at", "null"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "as of") {
		t.Errorf("page missing the reading age:\n%s", body)
	}
	if strings.Contains(body, "no readings yet") {
		t.Errorf("page should not claim there are no readings:\n%s", body)
	}
	if strings.Contains(body, "Setpoints:") {
		t.Errorf("a reading with freshly read setpoints should not carry a setpoints note:\n%s", body)
	}

	keys := []string{"battery_soc", "boost_ends_at", "grid_power_w", "set_charge_current"}
	prev := -1
	for _, key := range keys {
		at := strings.Index(body, key)
		if at <= prev {
			t.Fatalf("keys are not rendered in sorted order (%q at %d, previous at %d):\n%s", key, at, prev, body)
		}
		prev = at
	}
}

// TestRecordReadingRejectsNonObjects pins the contract RecordReading advertises:
// exactly one JSON object, or the reading is refused.
func TestRecordReadingRejectsNonObjects(t *testing.T) {
	rejected := []struct {
		name string
		doc  string
	}{
		{name: "empty", doc: ``},
		{name: "null", doc: `null`},
		{name: "array", doc: `[1]`},
		{name: "scalar", doc: `"x"`},
		{name: "malformed", doc: `{"battery_soc":`},
		{name: "trailing junk", doc: `{"a":1} junk`},
		{name: "two documents", doc: `{"a":1}{"b":2}`},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			s := New(Config{FailureThreshold: 1})
			if err := s.RecordReading(Reading{Doc: []byte(tc.doc)}); err == nil {
				t.Fatalf("RecordReading(%q) = nil, want an error", tc.doc)
			}
			if body := getRoot(t, s); !strings.Contains(body, "no readings yet") {
				t.Errorf("a refused reading must not be shown:\n%s", body)
			}
		})
	}

	// An empty object is a well-formed (if contentless) document and is accepted:
	// the page dates it and renders the table's headers alone.
	s := New(Config{FailureThreshold: 1})
	if err := s.RecordReading(Reading{Doc: []byte(`{}`)}); err != nil {
		t.Fatalf("RecordReading({}) = %v, want nil", err)
	}
	body := getRoot(t, s)
	if !strings.Contains(body, "as of") || !strings.Contains(body, "<table") {
		t.Errorf("an empty object should render a dated, headers-only table:\n%s", body)
	}
}

// TestRecordReadingKeepsThePreviousReadingOnError covers the failure mode that
// matters operationally: a bad document must not blank a page that was showing
// good values.
func TestRecordReadingKeepsThePreviousReadingOnError(t *testing.T) {
	s := New(Config{FailureThreshold: 1})
	recordReading(t, s, `{"battery_soc":57}`)

	if err := s.RecordReading(Reading{Doc: []byte(`{"battery_soc":`)}); err == nil {
		t.Fatal("RecordReading with a malformed document = nil, want an error")
	}

	body := getRoot(t, s)
	if !strings.Contains(body, "57") {
		t.Errorf("page should still show the last good reading:\n%s", body)
	}
	if strings.Contains(body, "no readings yet") {
		t.Errorf("a refused reading must not blank the page:\n%s", body)
	}
}

func TestRecordReadingDoesNotAliasTheDocument(t *testing.T) {
	s := New(Config{FailureThreshold: 1})
	doc := []byte(`{"battery_soc":57}`)
	if err := s.RecordReading(Reading{Doc: doc}); err != nil {
		t.Fatalf("RecordReading: %v", err)
	}
	copy(doc, []byte(`{"battery_soc":11}`))

	if body := getRoot(t, s); !strings.Contains(body, "57") {
		t.Errorf("the page must not follow a caller's later mutation of Doc:\n%s", body)
	}
}

func TestRootStatusPageRefreshInterval(t *testing.T) {
	tests := []struct {
		poll time.Duration
		want string
	}{
		{poll: 30 * time.Second, want: `content="30"`},
		{poll: 2 * time.Minute, want: `content="120"`},
	}
	for _, tc := range tests {
		s := New(Config{FailureThreshold: 1, PollInterval: tc.poll})
		body := getRoot(t, s)
		if !strings.Contains(body, `http-equiv="refresh"`) {
			t.Fatalf("poll %s: page missing the refresh meta tag:\n%s", tc.poll, body)
		}
		if !strings.Contains(body, tc.want) {
			t.Errorf("poll %s: page missing %s:\n%s", tc.poll, tc.want, body)
		}
	}

	// An unset interval (only reachable in tests; config enforces a 5 s floor)
	// emits no refresh tag rather than a zero-second reload loop.
	s := New(Config{FailureThreshold: 1})
	if body := getRoot(t, s); strings.Contains(body, `http-equiv="refresh"`) {
		t.Errorf("a zero poll interval should emit no refresh tag:\n%s", body)
	}
}

func TestRootStatusPageStaleSetpoints(t *testing.T) {
	s := New(Config{FailureThreshold: 1})
	recordReading(t, s, `{"battery_soc":57,"set_charge_current":2.5}`)
	if body := getRoot(t, s); strings.Contains(body, "Setpoints:") {
		t.Fatalf("a fresh reading should carry no setpoints note:\n%s", body)
	}

	if err := s.RecordReading(Reading{Doc: []byte(`{"battery_soc":58,"set_charge_current":2.5}`), SetpointsStale: true}); err != nil {
		t.Fatalf("RecordReading: %v", err)
	}
	body := getRoot(t, s)
	if !strings.Contains(body, "Setpoints: last read") {
		t.Errorf("a reading with reused setpoints should date them separately:\n%s", body)
	}
	if !strings.Contains(body, "holding registers unreadable since") {
		t.Errorf("the setpoints note should say why they are older:\n%s", body)
	}

	recordReading(t, s, `{"battery_soc":59,"set_charge_current":3}`)
	if body := getRoot(t, s); strings.Contains(body, "Setpoints:") {
		t.Errorf("a successful setpoints read should clear the note:\n%s", body)
	}
}

// TestRootStatusPageSetpointsNeverRead covers the first poll failing its setpoints
// read: there is no earlier successful read to date them from.
func TestRootStatusPageSetpointsNeverRead(t *testing.T) {
	s := New(Config{FailureThreshold: 1})
	if err := s.RecordReading(Reading{Doc: []byte(`{"battery_soc":57}`), SetpointsStale: true}); err != nil {
		t.Fatalf("RecordReading: %v", err)
	}
	if body := getRoot(t, s); !strings.Contains(body, "Setpoints: not yet read from the inverter") {
		t.Errorf("page should say the setpoints have never been read:\n%s", body)
	}
}

// recordReading records doc and fails the test if the server refuses it.
func recordReading(t *testing.T, s *Server, doc string) {
	t.Helper()
	if err := s.RecordReading(Reading{Doc: []byte(doc)}); err != nil {
		t.Fatalf("RecordReading(%s): %v", doc, err)
	}
}

// getRoot returns the status page body, failing the test unless / answers 200.
func getRoot(t *testing.T, s *Server) string {
	t.Helper()
	_, body := getRootResponse(t, s)
	return body
}

// getRootResponse fetches the status page from s behind an httptest server and
// returns the response (body already read and closed) alongside the body, for the
// tests that assert on headers as well as markup.
func getRootResponse(t *testing.T, s *Server) (*http.Response, string) {
	t.Helper()
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET / error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

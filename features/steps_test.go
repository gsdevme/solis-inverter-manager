package features

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cucumber/godog"

	"github.com/gsdevme/solis-inverter-manager/internal/server"
)

// world holds per-scenario state. The scaffold wires the real status server behind
// an httptest server and exercises its probes — no inverter, sidecar or broker.
type world struct {
	stat *server.Server
	srv  *httptest.Server
}

func (w *world) reset() {
	w.stat = nil
	w.srv = nil
}

func (w *world) cleanup() {
	if w.srv != nil {
		w.srv.Close()
	}
}

// --- Given / When ---

func (w *world) statusServerRunning() error {
	w.stat = server.New(server.Config{FailureThreshold: 3})
	w.srv = httptest.NewServer(w.stat.Handler())
	return nil
}

func (w *world) markedReady() error {
	w.stat.SetReady(true)
	return nil
}

// --- Then ---

func (w *world) endpointReturns(path string, want int) error {
	resp, err := http.Get(w.srv.URL + path)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		return fmt.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
	}
	return nil
}

func (w *world) livenessReturns(code int) error  { return w.endpointReturns("/healthz", code) }
func (w *world) readinessReturns(code int) error { return w.endpointReturns("/readyz", code) }

func TestFeatures(t *testing.T) {
	w := &world{}
	suite := godog.TestSuite{
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
				w.reset()
				return c, nil
			})
			ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
				w.cleanup()
				return c, err
			})

			ctx.Step(`^the manager status server is running$`, w.statusServerRunning)
			ctx.Step(`^the manager is marked ready$`, w.markedReady)
			ctx.Step(`^the liveness endpoint returns (\d+)$`, w.livenessReturns)
			ctx.Step(`^the readiness endpoint returns (\d+)$`, w.readinessReturns)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"."},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog acceptance suite failed")
	}
}

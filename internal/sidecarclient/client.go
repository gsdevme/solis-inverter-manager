package sidecarclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultTimeout bounds a single sidecar HTTP round-trip. It sits above the
// sidecar's own INVERTER_SOCKET_TIMEOUT (default 10s) so a slow-but-successful
// Modbus call is not cut short by the client.
const defaultTimeout = 15 * time.Second

// readRequest is the {addr, count} body for /read_input and /read_holding.
type readRequest struct {
	Addr  int `json:"addr"`
	Count int `json:"count"`
}

// readResponse is the {addr, count, regs} success body for a block read. Regs is
// count raw uint16 words in register order, undecoded.
type readResponse struct {
	Addr  int      `json:"addr"`
	Count int      `json:"count"`
	Regs  []uint16 `json:"regs"`
}

// writeRequest is the {addr, value} body for /write_holding.
type writeRequest struct {
	Addr  int    `json:"addr"`
	Value uint16 `json:"value"`
}

// writeResponse is the {addr, value, ok} success body for /write_holding.
type writeResponse struct {
	Addr  int    `json:"addr"`
	Value uint16 `json:"value"`
	OK    bool   `json:"ok"`
}

// Health is the decoded /health response.
type Health struct {
	// OK is sidecar process liveness (always true while serving).
	OK bool `json:"ok"`
	// InverterReachable is a cheap single-register probe of the datalogger; the
	// Go /readyz consumes it. Under MODE=mock it is always true.
	InverterReachable bool `json:"inverter_reachable"`
	// Mode is "mock" or "live".
	Mode string `json:"mode"`
}

// errorEnvelope is the {error:{code,message}} body every non-2xx response
// carries.
type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// httpDoer is the subset of *http.Client the transport needs, so tests can
// substitute a fake without a live server.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client is a typed HTTP client for the thin Python sidecar's register RPCs.
//
// It is a DUMB TRANSPORT: it moves raw register words and ack values across
// localhost HTTP and nothing more. It performs no register decode, scaling, sign
// interpretation or endianness handling — that is the internal/inverter
// package's job — and it enforces no read-before-write guard: WriteHolding
// always issues the fc06. The read-before-write guard that avoids flash wear is
// the manager's responsibility, layered above this client.
//
// One *http.Client is reused across all requests. Construct a Client with New.
type Client struct {
	baseURL string
	http    httpDoer
}

// Option configures a Client in New.
type Option func(*Client)

// WithHTTPClient sets the *http.Client used for every request, replacing the
// default. Use it to share a transport or tune timeouts and connection pooling.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.http = hc
		}
	}
}

// WithTimeout sets the per-request timeout on the default *http.Client. It is
// ignored when WithHTTPClient supplies a caller-owned client.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if hc, ok := c.http.(*http.Client); ok {
			hc.Timeout = d
		}
	}
}

// New returns a Client for the sidecar reachable at baseURL (the manager's
// resolved SIDECAR_URL, e.g. http://127.0.0.1:8081). It does not read the
// environment; the base URL and any HTTP-client tuning are supplied by the
// caller. Without options the client uses one *http.Client with a sensible
// default timeout.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: defaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ReadInput reads count input registers (fc04) starting at addr and returns the
// raw uint16 words in register order. Decoding is the caller's job: feed the
// result into inverter.Block{Base: addr, Regs: regs}.
func (c *Client) ReadInput(ctx context.Context, addr, count int) ([]uint16, error) {
	regs, err := c.read(ctx, "/read_input", addr, count)
	if err != nil {
		return nil, fmt.Errorf("read_input %d..%d: %w", addr, addr+count-1, err)
	}
	return regs, nil
}

// ReadHolding reads count holding registers (fc03) starting at addr and returns
// the raw uint16 words in register order. Decoding is the caller's job.
func (c *Client) ReadHolding(ctx context.Context, addr, count int) ([]uint16, error) {
	regs, err := c.read(ctx, "/read_holding", addr, count)
	if err != nil {
		return nil, fmt.Errorf("read_holding %d..%d: %w", addr, addr+count-1, err)
	}
	return regs, nil
}

// WriteHolding writes value to the single holding register at addr (fc06). The
// write is UNCONDITIONAL: the read-before-write guard that avoids needless flash
// wear lives in the manager, not here.
func (c *Client) WriteHolding(ctx context.Context, addr int, value uint16) error {
	var resp writeResponse
	if err := c.do(ctx, http.MethodPost, "/write_holding", writeRequest{Addr: addr, Value: value}, &resp); err != nil {
		return fmt.Errorf("write_holding %d=%d: %w", addr, value, err)
	}
	if !resp.OK {
		return fmt.Errorf("write_holding %d=%d: sidecar reported ok=false", addr, value)
	}
	return nil
}

// Health probes the sidecar's /health endpoint. InverterReachable feeds the Go
// /readyz gate.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	if err := c.do(ctx, http.MethodGet, "/health", nil, &h); err != nil {
		return Health{}, fmt.Errorf("health: %w", err)
	}
	return h, nil
}

// probeServing issues a bare GET /health and reports whether the sidecar
// answered with a 200. The body is deliberately NOT decoded: REQ-LC-11 ends the
// startup wait on any 200, so a sidecar whose /health body is malformed — or not
// JSON at all — still counts as serving. Health is not reused here precisely
// because it must keep its stricter decoding contract for InverterReachable.
func (c *Client) probeServing(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http GET /health: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return parseError(resp)
	}
	return nil
}

// WaitUntilServing blocks until the sidecar's HTTP listener answers /health,
// retrying every interval until then. It exists because the two-container pod
// starts the manager before the sidecar, so the first register call would
// otherwise fail with a connection refused.
//
// Any 200 ends the wait, whatever the body: the response is never decoded and
// InverterReachable is deliberately ignored, because the sidecar answering at
// all is what this waits for — inverter reachability is the poll loop's and
// /readyz's concern. When ctx ends first the last probe error is returned
// wrapped with ctx.Err().
func (c *Client) WaitUntilServing(ctx context.Context, every time.Duration) error {
	for {
		lastErr := c.probeServing(ctx)
		if lastErr == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for sidecar: %w: last error: %w", ctx.Err(), lastErr)
		case <-time.After(every):
		}
	}
}

// read performs a block read against path and returns the raw register words.
func (c *Client) read(ctx context.Context, path string, addr, count int) ([]uint16, error) {
	var resp readResponse
	if err := c.do(ctx, http.MethodPost, path, readRequest{Addr: addr, Count: count}, &resp); err != nil {
		return nil, err
	}
	return resp.Regs, nil
}

// do issues one request, decoding a 2xx JSON body into out (when non-nil) and
// mapping a non-2xx envelope to a *SidecarError. The response body is always
// drained and closed so the underlying connection can be reused.
func (c *Client) do(ctx context.Context, method, path string, reqBody, out any) error {
	var body io.Reader
	if reqBody != nil {
		encoded, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http %s %s: %w", method, path, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseError(resp)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// parseError turns a non-2xx response into a *SidecarError when the body carries
// a well-formed envelope, or a generic error including the status otherwise.
func parseError(resp *http.Response) error {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("http %s: read error body: %w", resp.Status, err)
	}

	var env errorEnvelope
	if json.Unmarshal(raw, &env) == nil && env.Error.Code != "" {
		return &SidecarError{
			Code:    env.Error.Code,
			Message: env.Error.Message,
			Status:  resp.StatusCode,
		}
	}
	return fmt.Errorf("http %s: unexpected response body", resp.Status)
}

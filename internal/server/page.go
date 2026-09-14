package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"time"
)

// serviceName is shown at the top of the status page.
const serviceName = "solis-inverter-manager"

// valueRow is one key/value pair of the last state document, rendered as a table
// row. Both sides are pre-formatted strings so the template does no conversion.
type valueRow struct {
	Key   string
	Value string
}

// statusView is the data rendered into the / page. It is built from a snapshot of
// the Server so the template never touches shared state directly.
type statusView struct {
	Service      string
	Ready        bool
	Uptime       string
	GoVersion    string
	PollInterval string
	// Refresh is the auto-refresh period in whole seconds; zero renders no
	// refresh tag at all.
	Refresh int
	// HaveReading is false until the first reading is recorded, and is the only
	// thing that decides between the value table and "no readings yet".
	HaveReading bool
	ValuesAge   string
	// Setpoints is a sentence about how current the reading's setpoints are, or
	// empty when they were read on the same pass as the telemetry.
	Setpoints string
	Values    []valueRow
}

// statusTmpl is parsed once at package load. The markup is deliberately minimal and
// isolated here so it is the single place to iterate on styling later.
var statusTmpl = template.Must(template.New("status").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
{{if .Refresh}}<meta http-equiv="refresh" content="{{.Refresh}}">
{{end}}<title>{{.Service}} status</title>
</head>
<body>
<h1>{{.Service}}</h1>
<p>Status: {{if .Ready}}ready{{else}}not ready{{end}}</p>
<p>Uptime: {{.Uptime}}</p>
<h2>Inverter values</h2>
{{if .HaveReading}}
<p>Last reading: as of {{.ValuesAge}} ago</p>
{{if .Setpoints}}<p>{{.Setpoints}}</p>
{{end}}<table>
<tr><th>Key</th><th>Value</th></tr>
{{range .Values}}<tr><td>{{.Key}}</td><td>{{.Value}}</td></tr>
{{end}}</table>
{{else}}
<p>Last reading: no readings yet</p>
{{end}}
<h2>Schedule</h2>
<ul>
<li>Poll interval: {{.PollInterval}}</li>
</ul>
<p>Go: {{.GoVersion}}</p>
</body>
</html>
`))

// handleRoot renders the HTML status page. It always returns 200 and never exposes
// credentials or the inverter serial.
func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	ready, last := s.snapshot()
	view := statusView{
		Service:      serviceName,
		Ready:        ready,
		Uptime:       time.Since(s.startedAt).Round(time.Second).String(),
		GoVersion:    runtime.Version(),
		PollInterval: s.cfg.PollInterval.String(),
		Refresh:      refreshSeconds(s.cfg.PollInterval),
	}
	if last != nil {
		view.HaveReading = true
		view.Values = last.rows
		view.ValuesAge = time.Since(last.at).Round(time.Second).String()
		view.Setpoints = setpointsLine(last)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = statusTmpl.Execute(w, view)
}

// setpointsLine says how current a reading's writable-control setpoints are. It is
// empty — and the page then says nothing extra — whenever they were read on the
// same pass as the telemetry, so the line appears only when the holding-register
// read has been failing and the page would otherwise imply the setpoints are as
// fresh as the reading's age.
func setpointsLine(r *reading) string {
	switch {
	case r.setpointsAt.IsZero():
		return "Setpoints: not yet read from the inverter"
	case r.setpointsAt.Equal(r.at):
		return ""
	default:
		age := time.Since(r.setpointsAt).Round(time.Second)
		return fmt.Sprintf("Setpoints: last read %s ago (holding registers unreadable since)", age)
	}
}

// refreshSeconds is the page's auto-refresh period in whole seconds for a given
// poll interval: the interval itself, or 0 (no refresh tag) for an interval under
// a second, which only an unset Config produces. The 5 s POLL_INTERVAL floor is
// enforced by config validation, which this package deliberately does not import.
func refreshSeconds(poll time.Duration) int {
	if poll < time.Second {
		return 0
	}
	return int(poll.Round(time.Second) / time.Second)
}

// decodeValues turns a flat JSON state document into table rows sorted by key.
// Numbers are kept as json.Number so they render exactly as published rather than
// through float64 formatting. The document must be exactly one JSON object: empty
// input, `null`, an array, a scalar, malformed JSON and anything trailing the
// object are all errors, so the caller can reject the reading and keep the previous
// one rather than serving a blank or broken page.
func decodeValues(doc []byte) ([]valueRow, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()

	var fields map[string]any
	if err := dec.Decode(&fields); err != nil {
		return nil, fmt.Errorf("decode state document: %w", err)
	}
	if fields == nil {
		return nil, errors.New("state document is JSON null, want an object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after the state document")
	}

	rows := make([]valueRow, 0, len(fields))
	for key, value := range fields {
		rows = append(rows, valueRow{Key: key, Value: formatValue(value)})
	}
	slices.SortFunc(rows, func(a, b valueRow) int { return strings.Compare(a.Key, b.Key) })
	return rows, nil
}

// formatValue renders one decoded JSON value for the table, spelling null as JSON
// does rather than as fmt's "<nil>".
func formatValue(value any) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprint(value)
}

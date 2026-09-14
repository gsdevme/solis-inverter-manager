package server

import (
	"html/template"
	"net/http"
	"runtime"
	"time"
)

// serviceName is shown at the top of the status page.
const serviceName = "solis-inverter-manager"

// statusView is the data rendered into the / page. It is built from a snapshot of
// the Server so the template never touches shared state directly.
type statusView struct {
	Service      string
	Ready        bool
	Uptime       string
	GoVersion    string
	PollInterval string
}

// statusTmpl is parsed once at package load. The markup is deliberately minimal and
// isolated here so it is the single place to iterate on styling later.
//
// TODO(phase-2+): surface the latest inverter reading (SOC, battery/grid power,
// setpoints) and the sidecar/MQTT connection state once those exist.
var statusTmpl = template.Must(template.New("status").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Service}} status</title>
</head>
<body>
<h1>{{.Service}}</h1>
<p>Status: {{if .Ready}}ready{{else}}not ready{{end}}</p>
<p>Uptime: {{.Uptime}}</p>
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
	view := statusView{
		Service:      serviceName,
		Ready:        s.Ready(),
		Uptime:       time.Since(s.startedAt).Round(time.Second).String(),
		GoVersion:    runtime.Version(),
		PollInterval: s.cfg.PollInterval.String(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = statusTmpl.Execute(w, view)
}

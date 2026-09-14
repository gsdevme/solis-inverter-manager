package cmd

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gsdevme/solis-inverter-manager/internal/homeassistant"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/publisher"
	"github.com/gsdevme/solis-inverter-manager/internal/schedule"
	"github.com/gsdevme/solis-inverter-manager/internal/server"
)

// readingRecorder is the status page's record seam, satisfied by *server.Server.
// Taking the interface keeps statePublisher testable against a stub and states
// exactly how much of the server this file uses.
type readingRecorder interface {
	RecordReading(server.Reading) error
}

// statePublisher is the scheduler's StatePublisher and the command refresh's
// publish step: the single place a freshly read state fans out to its consumers.
// It publishes through the Service (Home Assistant, or Discard without a broker)
// and records the built document for the status page, so / and HA show the same
// reading. A publish failure is returned after the reading has been recorded,
// because the page reports the inverter, not the broker. Without a broker it also
// logs the decoded telemetry so a mock run stays observable.
type statePublisher struct {
	svc          func() *publisher.Service
	status       readingRecorder
	fresh        *setpointFreshness
	logTelemetry bool
	logger       *slog.Logger
}

// PublishState builds the state document once, publishes it and hands the same
// bytes to the status page.
func (p *statePublisher) PublishState(ctx context.Context, tel inverter.Telemetry, sp homeassistant.Setpoints) error {
	if p.logTelemetry {
		p.logger.Info("telemetry",
			"time", tel.Time,
			"battery_soc", tel.Battery.SOCPercent,
			"battery_power_w", tel.Battery.PowerW,
			"pv_power_w", tel.PV.TotalPowerW,
			"grid_power_w", tel.Grid.PowerW,
			"set_charge_current", sp.SetChargeCurrent,
			"set_discharge_current", sp.SetDischargeCurrent,
			"optimal_income", sp.OptimalIncome,
			"tou_window", touWindowAttr(sp.Slots),
			"boost", schedule.BoostOf(sp.Slots).String(),
			"boost_select", boostSelectAttr(sp.Slots),
		)
	}

	svc := p.svc()
	if svc == nil {
		return errors.New("publisher not connected")
	}

	msg, err := svc.PublishState(ctx, tel, sp)
	if len(msg.Payload) > 0 {
		reading := server.Reading{Doc: msg.Payload, SetpointsStale: p.fresh.isStale()}
		if recErr := p.status.RecordReading(reading); recErr != nil {
			p.logger.Warn("recording the reading for the status page failed", "err", recErr)
		}
	}
	return err
}

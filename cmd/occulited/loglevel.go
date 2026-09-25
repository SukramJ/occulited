package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hobbyquaker/occulited/internal/config"
	"github.com/hobbyquaker/occulited/internal/logctl"
	"github.com/hobbyquaker/occulited/internal/priv"
)

// ownLogLevel is occulited's own level behind the Log settings (task 101): stored in
// occulited.json, so it survives a restart and travels in a backup, applied to every logger at
// once through the control, and handed to the root helper so its lines follow.
type ownLogLevel struct {
	ctl     *logctl.Control
	cfgPath string
	// helper is the privilege helper's client; nil when occulited runs as root and has none.
	helper *priv.Client
	log    *slog.Logger
	// stored follows a stored setting into the configuration main keeps in memory, which other
	// switches save whole; without it such a save would put the old level back.
	stored func(logctl.Setting)
}

func (o *ownLogLevel) Get() logctl.Setting { return o.ctl.Get() }

// Set stores the setting first - read, change, write, so a change another route made to the file
// in the meantime is kept - then applies it; a setting that cannot be stored is not applied.
func (o *ownLogLevel) Set(ctx context.Context, s logctl.Setting) (logctl.Setting, error) {
	n, err := logctl.Normalize(s)
	if err != nil {
		return logctl.Setting{}, err
	}
	if o.cfgPath != "" {
		cfg, err := config.Load(o.cfgPath)
		if err != nil {
			return logctl.Setting{}, fmt.Errorf("config: %w", err)
		}
		cfg.LogLevel, cfg.LogDebugAreas = n.Level, n.DebugAreas
		if len(n.DebugAreas) == 0 {
			cfg.LogDebugAreas = nil
		}
		if err := config.Save(o.cfgPath, cfg); err != nil {
			return logctl.Setting{}, fmt.Errorf("config: %w", err)
		}
		if o.stored != nil {
			o.stored(n)
		}
	}
	was := o.ctl.Get()
	if n, err = o.ctl.Set(n); err != nil {
		return logctl.Setting{}, err
	}
	if was.Level != n.Level || fmt.Sprint(was.DebugAreas) != fmt.Sprint(n.DebugAreas) {
		o.log.Info("log level of occulited changed", "level", n.Level, "debug_areas", n.DebugAreas)
	}
	o.pushToHelper(ctx, n.Level)
	return n, nil
}

// pushToHelper sets the helper's level; a helper that cannot take it keeps its own and says so
// in occulited's log - the daemon's level is in effect either way.
func (o *ownLogLevel) pushToHelper(ctx context.Context, level string) {
	if o.helper == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := o.helper.SetLogLevel(ctx, level); err != nil {
		o.log.Warn("the privilege helper did not take the log level", "level", level, "err", err)
	}
}

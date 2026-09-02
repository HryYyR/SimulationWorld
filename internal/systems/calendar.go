package systems

import (
	"ecosim/internal/core"
	"ecosim/internal/env"
	"ecosim/internal/world"
)

type AdvanceCalendar struct{}

func (s *AdvanceCalendar) Name() string { return "advanceCalendar" }

func (s *AdvanceCalendar) Step(w *world.World, c *core.Ctx) {
	old := env.SeasonOf(w.Tick, c.Cfg.Balance.Time.TicksPerSeason)
	w.Tick++
	c.Tick = w.Tick
	next := env.SeasonOf(w.Tick, c.Cfg.Balance.Time.TicksPerSeason)
	if old != next {
		c.Ev.Emit(w.Tick, "season_changed", int(old), int(next), float64(w.Tick))
	}
}

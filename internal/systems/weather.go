package systems

import (
	"ecosim/internal/core"
	"ecosim/internal/env"
	"ecosim/internal/rng"
	"ecosim/internal/world"
)

type StepClimate struct{}

func (s *StepClimate) Name() string { return "stepClimate" }

func (s *StepClimate) Step(w *world.World, c *core.Ctx) {
	r := rng.New(w.Seed, rng.StreamWeather, w.Tick, 0)
	old := env.SeasonOf(w.Tick, w.Climate.SeasonBounds)
	env.StepClimate(&w.Climate, w.Tick, c.Cfg, r)
	next := env.SeasonOf(w.Tick, w.Climate.SeasonBounds)
	if old != next {
		c.Ev.Emit(w.Tick, "season_changed", int(old), int(next), float64(w.Tick))
	}
}

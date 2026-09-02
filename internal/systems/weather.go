package systems

import (
	"ecosim/internal/core"
	"ecosim/internal/env"
	"ecosim/internal/rng"
	"ecosim/internal/world"
)

type StepWeather struct{}

func (s *StepWeather) Name() string { return "stepWeather" }

func (s *StepWeather) Step(w *world.World, c *core.Ctx) {
	r := rng.New(w.Seed, rng.StreamWeather, w.Tick, 0)
	season := env.SeasonOf(w.Tick, c.Cfg.Balance.Time.TicksPerSeason)
	next, changed := env.AdvanceWeather(c.Cfg, season, w.Weather, r)
	if changed {
		w.Weather = next
		c.Ev.Emit(w.Tick, "weather_changed", 0, 0, float64(weatherIndex(c, next.Current)))
	} else {
		w.Weather = next
	}
}

func weatherIndex(c *core.Ctx, state string) int {
	for i, s := range c.Cfg.Weather.States {
		if s == state {
			return i
		}
	}
	return -1
}

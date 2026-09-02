package systems

import (
	"ecosim/internal/core"
	"ecosim/internal/env"
	"ecosim/internal/modifier"
	"ecosim/internal/world"
)

type ResolveModifiers struct{}

func (s *ResolveModifiers) Name() string { return "resolveModifiers" }

func (s *ResolveModifiers) Step(w *world.World, c *core.Ctx) {
	season := env.SeasonOf(w.Tick, c.Cfg.Balance.Time.TicksPerSeason)
	globals := append([]modifier.Modifier(nil), env.WeatherModifiers(c.Cfg, w.Weather.Current)...)
	globals = append(globals, env.SeasonModifiers(c.Cfg, season)...)
	scoped := make([]modifier.Scoped, 0)

	retained := make([]modifier.Timed, 0, len(c.Timed))
	for _, timed := range c.Timed {
		if timed.TTL <= 0 {
			continue
		}
		if timed.R > 0 {
			scoped = append(scoped, modifier.Scoped{Modifier: timed.Modifier, X: timed.X, Y: timed.Y, R: timed.R})
		} else {
			globals = append(globals, timed.Modifier)
		}
		timed.TTL--
		if timed.TTL > 0 {
			retained = append(retained, timed)
		}
	}
	c.Timed = retained
	c.Params = modifier.Resolve(c.Cfg.BaseSlots(), globals, scoped)
	c.Scoped = scoped
}

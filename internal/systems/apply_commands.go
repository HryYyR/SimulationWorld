package systems

import (
	"ecosim/internal/core"
	"ecosim/internal/env"
	"ecosim/internal/modifier"
	"ecosim/internal/rng"
	"ecosim/internal/world"
)

type ApplyCommands struct{}

func (s *ApplyCommands) Name() string { return "applyCommands" }

func (s *ApplyCommands) Step(w *world.World, c *core.Ctx) {
	core.SortCommands(c.Commands)
	i := 0
	for ; i < len(c.Commands); i++ {
		cmd := c.Commands[i]
		if cmd.Tick > w.Tick {
			break
		}
		s.apply(w, c, cmd)
	}
	c.Commands = c.Commands[i:]
}

func (s *ApplyCommands) apply(w *world.World, c *core.Ctx, cmd core.Command) {
	switch cmd.Type {
	case "spawn":
		if cmd.Spawn == nil {
			return
		}
		if _, ok := c.Cfg.Species[cmd.Spawn.Species]; !ok || !w.Grid.InBounds(cmd.Spawn.X, cmd.Spawn.Y) {
			return
		}
		sp := c.Cfg.Species[cmd.Spawn.Species]
		energy := 60.0
		if cmd.Spawn.Species == "tiger" {
			energy = 80
		}
		lr := rng.New(w.Seed, rng.StreamLifespan, w.Tick, w.NextID)
		a := w.AddAnimal(cmd.Spawn.Species, &sp, cmd.Spawn.X, cmd.Spawn.Y, energy, lr.IntRange(sp.Lifespan[0], sp.Lifespan[1]))
		c.Ledger.Add("god.spawn", energy)
		c.Metrics.Birth()
		c.Ev.Emit(w.Tick, "born", a.ID, 0, energy)
	case "remove":
		if cmd.Remove == nil {
			return
		}
		if a := w.Animal(cmd.Remove.EntityID); a != nil {
			c.Ledger.Add("god.remove", -a.Energy)
			w.RemoveAnimal(a.ID)
			c.Ev.Emit(w.Tick, "removed", a.ID, cmd.ID, a.Energy)
		}
	case "weather_force":
		if cmd.WeatherForce == nil || env.ValidateState(c.Cfg, cmd.WeatherForce.State) != nil {
			return
		}
		left := cmd.WeatherForce.Ticks
		if left <= 0 {
			left = 1
		}
		w.Weather = env.WeatherState{Current: cmd.WeatherForce.State, Left: left}
		c.Ev.Emit(w.Tick, "weather_changed", 0, cmd.ID, float64(weatherIndex(c, cmd.WeatherForce.State)))
	case "param_mod":
		if cmd.ParamMod == nil || cmd.ParamMod.TTL <= 0 || cmd.ParamMod.Key == "" {
			return
		}
		m := modifier.Modifier{
			Key: cmd.ParamMod.Key, Mult: cmd.ParamMod.Mult,
			Source: godSource(cmd.ID), Priority: 100,
		}
		if cmd.ParamMod.R > 0 {
			c.Timed = append(c.Timed, modifier.Timed{
				Modifier: m, TTL: cmd.ParamMod.TTL,
				X: cmd.ParamMod.X, Y: cmd.ParamMod.Y, R: cmd.ParamMod.R,
			})
		} else {
			c.Timed = append(c.Timed, modifier.Timed{Modifier: m, TTL: cmd.ParamMod.TTL})
		}
	}
	c.Ev.Emit(w.Tick, "command_applied", cmd.ID, 0, float64(cmd.Tick))
}

func godSource(id int) string {
	return "god.cmd#" + itoa(id)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

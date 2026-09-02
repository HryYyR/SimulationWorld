package systems

import (
	"sort"

	"ecosim/internal/core"
	"ecosim/internal/world"
)

type ResolveDeaths struct{}

func (s *ResolveDeaths) Name() string { return "resolveDeaths" }

func (s *ResolveDeaths) Step(w *world.World, c *core.Ctx) {
	animals := append([]*world.Animal(nil), w.Animals...)
	sort.Slice(animals, func(i, j int) bool { return animals[i].ID < animals[j].ID })
	for _, a := range animals {
		if !a.Dead && a.Energy <= 0 {
			a.Dead = true
			a.Cause = "starved"
		}
		if !a.Dead && a.Age >= a.Lifespan {
			a.Dead = true
			a.Cause = "old_age"
		}
		if !a.Dead {
			continue
		}
		if a.Cause == "predated" {
			// The corpse and death event were already produced by the hunt.
			continue
		}
		// 饿死（能量耗尽）不生成尸体；老死生成尸体，量等于残余能量。
		// 无论是否生成尸体，死亡个体的残余能量都要记入守恒账。
		if a.Cause == "old_age" && a.Energy > 0 {
			sp := c.Cfg.Species[a.Species]
			w.AddCorpse(a.Species, a.X, a.Y, a.Energy, sp.Corpse.Ticks)
			c.Ledger.Add("corpse.residual", -a.Energy)
		} else if a.Energy != 0 {
			c.Ledger.Add("corpse.residual", -a.Energy)
		}
		c.Ev.Emit(w.Tick, "died", a.ID, 0, deathCode(a.Cause))
		if a.Cause == "starved" {
			c.Metrics.Starved()
		} else {
			c.Metrics.OldAge()
		}
	}

	alive := make([]*world.Animal, 0, len(animals))
	for _, a := range animals {
		if !a.Dead {
			alive = append(alive, a)
		}
	}
	w.Animals = alive
	w.RebuildIndex()
	for _, a := range w.Animals {
		a.Age++
	}
}

func deathCode(cause string) float64 {
	switch cause {
	case "starved":
		return 1
	case "old_age":
		return 2
	case "predated":
		return 3
	default:
		return 0
	}
}

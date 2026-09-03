package systems

import (
	"math"
	"sort"

	"ecosim/internal/core"
	"ecosim/internal/world"
)

type DecayCorpses struct{}

func (s *DecayCorpses) Name() string { return "decayCorpses" }

func (s *DecayCorpses) Step(w *world.World, c *core.Ctx) {
	if len(w.Corpses) == 0 {
		return
	}
	corpses := append([]*world.Corpse(nil), w.Corpses...)
	sort.Slice(corpses, func(i, j int) bool { return corpses[i].ID < corpses[j].ID })
	nutrientCap := c.Params.Get("grass.nutrient_cap")
	decayMult := c.Params.Get("corpse.decay_mult")
	retained := make([]*world.Corpse, 0, len(corpses))

	for _, corpse := range corpses {
		// 腐烂速度受气候（温度）修饰：高温加速、低温减速
		release := (corpse.Total / float64(corpse.TotalTicks)) * decayMult
		release = math.Min(release, corpse.Remaining)
		s.addNutrient(w, corpse.X, corpse.Y, release*0.7, nutrientCap)
		share := release * 0.3 / 8
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				if dx == 0 && dy == 0 {
					continue
				}
				s.addNutrient(w, corpse.X+dx, corpse.Y+dy, share, nutrientCap)
			}
		}
		corpse.Remaining -= release
		corpse.TicksLeft--
		if corpse.TicksLeft <= 0 || corpse.Remaining <= 1e-12 {
			c.Ev.Emit(w.Tick, "corpse_decayed", corpse.ID, 0, corpse.Remaining)
			continue
		}
		retained = append(retained, corpse)
	}
	w.Corpses = retained
}

func (s *DecayCorpses) addNutrient(w *world.World, x, y int, amount, cap float64) {
	if amount <= 0 || !w.Grid.InBounds(x, y) {
		return
	}
	i := w.Grid.Idx(x, y)
	w.Grid.Nutrient[i] += amount
	if cap > 0 && w.Grid.Nutrient[i] > cap {
		w.Grid.Nutrient[i] = cap
	}
}

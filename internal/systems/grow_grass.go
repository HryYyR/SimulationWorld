package systems

import (
	"math"

	"ecosim/internal/core"
	"ecosim/internal/world"
)

type GrowGrass struct{}

func (s *GrowGrass) Name() string { return "growGrass" }

func (s *GrowGrass) Step(w *world.World, c *core.Ctx) {
	base := c.Params.Get("grass.growth_base")
	coeff := c.Params.Get("grass.growth_nutrient_coeff")
	cap := c.Params.Get("grass.growth_cap")
	nutrientCap := c.Params.Get("grass.nutrient_cap")
	consumption := c.Params.Get("grass.nutrient_consumption_coeff")
	scoped := c.Params.ScopedFor("grass.growth_mult")

	for y := 0; y < w.Grid.H; y++ {
		for x := 0; x < w.Grid.W; x++ {
			mult := 1.0
			for _, m := range scoped {
				if m.R <= 0 || chebyshev(x, y, m.X, m.Y) <= m.R {
					mult *= m.Mult
				}
			}
			i := w.Grid.Idx(x, y)
			growth := (base + w.Grid.Nutrient[i]*coeff) * mult
			if growth < 0 {
				growth = 0
			}
			newGrass := math.Min(cap, w.Grid.Grass[i]+growth)
			actual := newGrass - w.Grid.Grass[i]
			w.Grid.Grass[i] = newGrass
			w.Grid.Nutrient[i] = math.Max(0, w.Grid.Nutrient[i]-actual*consumption)
			if nutrientCap > 0 {
				w.Grid.Nutrient[i] = math.Min(nutrientCap, w.Grid.Nutrient[i])
			}
			c.Ledger.Add("solar.grass", actual)
		}
	}
}

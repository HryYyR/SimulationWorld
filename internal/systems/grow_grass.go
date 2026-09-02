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
	bankMult := c.Params.Get("grass.river_bank_growth_mult")
	bankRadius := int(c.Params.Get("grass.river_bank_growth_radius"))

	for y := 0; y < w.Grid.H; y++ {
		for x := 0; x < w.Grid.W; x++ {
			i := w.Grid.Idx(x, y)
			if w.Grid.IsRiver(x, y) {
				continue // 河流不长草
			}
			mult := 1.0
			for _, m := range scoped {
				if m.R <= 0 || chebyshev(x, y, m.X, m.Y) <= m.R {
					mult *= m.Mult
				}
			}
			// 河流岸边：切比雪夫距离 bankRadius 内有河流则草加速（水边植被更茂盛）
			if bankRadius > 0 && bankMult != 1.0 && w.Grid.NearRiver(x, y, bankRadius) {
				mult *= bankMult
			}
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

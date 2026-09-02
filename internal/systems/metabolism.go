package systems

import (
	"ecosim/internal/core"
	"ecosim/internal/world"
)

type Metabolism struct{}

func (s *Metabolism) Name() string { return "metabolism" }

func (s *Metabolism) Step(w *world.World, c *core.Ctx) {
	for _, a := range w.Animals {
		if a.Dead {
			continue
		}
		sp := a.Sp
		mult := 1.0
		mature := sp.Reproduce.MatureAge
		if a.Age < mature {
			// 幼年：代谢较低
			mult = sp.JuvenileMetabolismMult
		} else if a.Age >= int(float64(a.Lifespan)*0.7) {
			// 老年：代谢升高
			mult = sp.OldMetabolismMult
		}
		// 代谢值可能被天气/季节动态修饰（如 winter 增加 deer 代谢），需查表
		base := c.Params.Get(a.Species + ".metabolism")
		amount := base * mult
		a.Energy -= amount
		c.Ledger.Add(a.Species+".metabolism", -amount)

		// 饱食惰性状态迁移：吃饱（≥ full×cap）进入惰性，饿（≤ hungry×cap）解除
		cap := energyCap(c, a)
		if full := sp.SatiationFull; full > 0 && a.Energy >= cap*full {
			a.Satiated = true
		}
		if hungry := sp.SatiationHungry; hungry > 0 && a.Energy <= cap*hungry {
			a.Satiated = false
		}
	}
}

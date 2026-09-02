package systems

import (
	"ecosim/internal/core"
	"ecosim/internal/world"
)

// energyCap 返回个体当前年龄的能量上限：幼年体型小、储备少，上限按
// juvenile_energy_cap_mult 打折；成年后达到满额。
// 参数均为静态（不随天气/季节变化），用 a.Sp 缓存，避免每 tick 字符串拼接查表。
func energyCap(c *core.Ctx, a *world.Animal) float64 {
	cap := c.Params.Get("global.energy_cap")
	if a.Age < a.Sp.Reproduce.MatureAge {
		cap *= a.Sp.JuvenileEnergyCapMult
	}
	return cap
}

func chebyshev(x1, y1, x2, y2 int) int {
	dx := x1 - x2
	if dx < 0 {
		dx = -dx
	}
	dy := y1 - y2
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return dx
	}
	return dy
}

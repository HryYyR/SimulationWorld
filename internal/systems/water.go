package systems

import (
	"ecosim/internal/core"
	"ecosim/internal/env"
	"ecosim/internal/world"
)

// StepWater 河流水量与季节强绑定：河岸边缘格（越靠近草地）在旱季水量逐渐衰减趋 0
// （形成淤泥/干涸河床），湿季恢复。中心格水量始终较高，旱季仅缓慢下降。
type StepWater struct{}

func (s *StepWater) Name() string { return "stepWater" }

func (s *StepWater) Step(w *world.World, c *core.Ctx) {
	season := env.SeasonOf(w.Tick, w.Climate.SeasonBounds)
	dry := season == env.SeasonDryEarly || season == env.SeasonDryLate

	for y := 0; y < w.Grid.H; y++ {
		for x := 0; x < w.Grid.W; x++ {
			if !w.Grid.IsRiver(x, y) {
				continue
			}
			i := w.Grid.Idx(x, y)
			if w.Grid.IsRiverEdge(x, y) {
				// 河岸边缘：旱季快速干涸（趋 0），湿季恢复
				if dry {
					w.Grid.Water[i] -= 1.2
				} else {
					w.Grid.Water[i] += 1.5
				}
			} else {
				// 中心格：旱季缓慢下降，湿季恢复满
				if dry {
					w.Grid.Water[i] -= 0.15
				} else {
					w.Grid.Water[i] += 0.3
				}
			}
			// 夹紧到 [0, 100]
			if w.Grid.Water[i] < 0 {
				w.Grid.Water[i] = 0
			}
			if w.Grid.Water[i] > 100 {
				w.Grid.Water[i] = 100
			}
		}
	}
}

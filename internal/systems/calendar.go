package systems

import (
	"ecosim/internal/core"
	"ecosim/internal/world"
)

type AdvanceCalendar struct{}

func (s *AdvanceCalendar) Name() string { return "advanceCalendar" }

// Step 只推进全局 tick。季节检测与切换事件统一由 StepClimate 负责（季节由气候分段决定）。
func (s *AdvanceCalendar) Step(w *world.World, c *core.Ctx) {
	w.Tick++
	c.Tick = w.Tick
}

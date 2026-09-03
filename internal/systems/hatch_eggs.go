package systems

import (
	"sort"

	"ecosim/internal/core"
	"ecosim/internal/rng"
	"ecosim/internal/world"
)

// HatchEggs 处理鳄鱼蛋的孵化：倒计时递减，到 0 时在蛋周围产出 0~3 只幼崽。
type HatchEggs struct{}

func (s *HatchEggs) Name() string { return "hatchEggs" }

func (s *HatchEggs) Step(w *world.World, c *core.Ctx) {
	if len(w.Eggs) == 0 {
		return
	}
	eggs := append([]*world.Egg(nil), w.Eggs...)
	sort.Slice(eggs, func(i, j int) bool { return eggs[i].ID < eggs[j].ID })
	retained := make([]*world.Egg, 0, len(eggs))

	for _, egg := range eggs {
		egg.TicksLeft--
		if egg.TicksLeft > 0 {
			retained = append(retained, egg)
			continue
		}
		s.hatch(w, c, egg)
		c.Ev.Emit(w.Tick, "hatched", egg.ID, 0, 0)
	}
	w.Eggs = retained
}

// hatch 在蛋周围找一个可进入且未被占用的格子产出幼崽，数量 0~3 随机。
// 蛋通常产在岸边陆地，而幼崽（如鳄鱼）只能进入水域，故搜索范围逐步扩大到能找到
// 该物种可进入的格子（对鳄鱼即附近的水格）为止。
func (s *HatchEggs) hatch(w *world.World, c *core.Ctx, egg *world.Egg) {
	sp, ok := c.Cfg.Species[egg.Species]
	if !ok {
		return
	}
	r := rng.New(w.Seed, rng.StreamBehavior, w.Tick, egg.ID)
	n := r.Intn(4) // 0~3 只幼崽
	// 蛋的能量分配给 n 只幼崽（能量守恒：蛋域 → 动物域）
	perChild := 0.0
	if n > 0 {
		perChild = egg.Energy / float64(n)
	}
	born := 0
	for k := 0; k < n; k++ {
		x, y := -1, -1
		for radius := 0; radius <= 6 && x < 0; radius++ {
			for dy := -radius; dy <= radius; dy++ {
				for dx := -radius; dx <= radius; dx++ {
					if dx*dx+dy*dy > radius*radius {
						continue
					}
					nx, ny := egg.X+dx, egg.Y+dy
					if !w.Grid.InBounds(nx, ny) {
						continue
					}
					if !w.CanEnter(nx, ny, &sp) {
						continue
					}
					if s.occupied(w, nx, ny) {
						continue
					}
					x, y = nx, ny
					break
				}
				if x >= 0 {
					break
				}
			}
		}
		if x < 0 {
			break // 无空位，剩余幼崽不再产出
		}
		lifespan := rng.New(w.Seed, rng.StreamLifespan, w.Tick, egg.ID+k).IntRange(sp.Lifespan[0], sp.Lifespan[1])
		w.AddAnimal(egg.Species, &sp, x, y, perChild, lifespan)
		born++
	}
	// 只有实际孵化出幼崽时才把蛋的能量记回动物域（n=0 时能量随蛋消散在蛋域，无需记账）
	if born > 0 {
		c.Ledger.Add(egg.Species+".egg", perChild*float64(born))
	}
}

func (s *HatchEggs) occupied(w *world.World, x, y int) bool {
	for _, a := range w.Animals {
		if !a.Dead && a.X == x && a.Y == y {
			return true
		}
	}
	return false
}

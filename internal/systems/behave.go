package systems

import (
	"math"
	"sort"

	"ecosim/internal/core"
	"ecosim/internal/rng"
	"ecosim/internal/world"
)

type Behave struct{}

var directions = [8][2]int{
	{-1, -1}, {0, -1}, {1, -1},
	{-1, 0}, {1, 0},
	{-1, 1}, {0, 1}, {1, 1},
}

func (s *Behave) Name() string { return "behave" }

func (s *Behave) Step(w *world.World, c *core.Ctx) {
	animals := append([]*world.Animal(nil), w.Animals...)
	sort.Slice(animals, func(i, j int) bool {
		return rng.Hash(w.Seed, rng.StreamOrder, uint64(w.Tick), uint64(animals[i].ID)) <
			rng.Hash(w.Seed, rng.StreamOrder, uint64(w.Tick), uint64(animals[j].ID))
	})
	for _, a := range animals {
		if a.Dead {
			continue
		}
		a.Moved = false // 每 tick 重置，供静止代谢判定
		r := rng.New(w.Seed, rng.StreamBehavior, w.Tick, a.ID)
		if a.Species == "deer" {
			s.deer(w, c, a, r)
		} else if a.Species == "tiger" {
			s.tiger(w, c, a, r)
		} else if a.Species == "crocodile" {
			s.crocodile(w, c, a, r)
		}
	}
}

func (s *Behave) deer(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) {
	threatRadius := a.Sp.ThreatRadius
	if threat := s.nearest(c, a, threatRadius, "tiger"); threat != nil {
		dx, dy := sign(a.X-threat.X), sign(a.Y-threat.Y)
		if dx == 0 && dy == 0 {
			d := directions[r.Intn(len(directions))]
			dx, dy = d[0], d[1]
		}
		s.move(w, c, a, dx, dy)
		return
	}
	// 幼崽：饥饿优先觅食，饱腹则跟随成年同类
	if s.isJuvenile(c, a) {
		if s.juvenileDeer(w, c, a, r) {
			return
		}
	}
	if s.eat(w, c, a) {
		return
	}
	if a.Energy < a.Sp.EatThreshold {
		s.forage(w, c, a, r)
		return
	}
	if s.tryReproduce(w, c, a) {
		return
	}
	s.wander(w, c, a, r)
}

// juvenileDeer 幼鹿行为：饥饿时优先吃草/觅食，饱腹时跟随最近成年同类。
func (s *Behave) juvenileDeer(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) bool {
	if s.isHungry(c, a) {
		if s.eat(w, c, a) {
			return true
		}
		s.forage(w, c, a, r)
		return true
	}
	if s.followAdult(w, c, a) {
		return true
	}
	return false
}

func (s *Behave) tiger(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) {
	// 优先级 1（最高）：不饱腹（饥饿或中间态）就正常捕杀 —— 不饱就开始找猎物，
	// 不受捕杀冷却限制（饿了就得吃，这是刚需）。
	if !a.Satiated {
		if s.huntOrScavenge(w, c, a, r) {
			return
		}
		s.wander(w, c, a, r)
		return
	}
	if s.tryReproduce(w, c, a) {
		return
	}
	// 幼崽：饥饿优先觅食（吃尸体/捕猎），饱腹则跟随成年同类
	if s.isJuvenile(c, a) {
		if s.juvenileTiger(w, c, a, r) {
			return
		}
	}
	// 优先级 2：为幼崽捕杀 —— 饱腹但有幼崽要喂且附近无尸体，不受冷却限制（喂崽是刚需）
	if s.provideForYoung(w, c, a, r) {
		return
	}
	// 优先级 3：空闲顺手捕杀 —— 饱腹且无幼崽时，鹿靠太近（kill_radius 内）就顺手杀，
	// 但受捕杀冷却限制：刚顺手杀过需歇歇，不能见一个杀一个，避免屠戮周围鹿。
	if a.HuntCool <= 0 {
		if killR := a.Sp.KillRadius; killR > 0 {
			if prey := s.nearest(c, a, killR, "deer"); prey != nil {
				s.hunt(w, c, a, prey, r)
				return
			}
		}
	}
	s.wander(w, c, a, r)
}

// crocodile 鳄鱼行为：平时只能在水里活动（白名单限制），行动迟缓、静止时代谢极低。
// 幼崽与成体逻辑：
//   - 幼崽：饥饿时也捕猎（取消"幼崽不捕猎"），饱腹时跟随成年体（靠近后稳定不乱动）
//   - 成体：范围内有鹿就捕猎（不依赖饥饿/饱食），吃满饱食度；饱腹产蛋；否则待机
//   - 最高优先级：若已上岸（突袭残留），立即回水，杜绝滞留陆地
func (s *Behave) crocodile(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) {
	// 优先级 0：已上岸则立即回水（修复上岸后卡在陆地不回水的 bug）
	if !w.Grid.IsRiver(a.X, a.Y) {
		s.returnToWater(w, c, a)
		return
	}

	ambush := a.Sp.AmbushRadius
	if ambush <= 0 {
		ambush = 1
	}
	cap := energyCap(c, a)
	// 吃满阈值：能量达到 cap*satiation_full 即视为"吃满"，停止捕猎；
	// 只有掉回该阈值以下才重新捕猎，避免能量在满额附近反复捕猎、频繁上岸。
	full := cap
	if f := a.Sp.SatiationFull; f > 0 {
		full = cap * f
	}
	needFood := a.Energy < full

	// 幼崽：跟随成年体为主，绝不主动巡游（刚出生乱跑反而危险）。
	// 有成年体就跟随；找不到成年体时原地等待（由成年体后续寻回/供食），
	// 仅在眼前就有鹿（伏击范围内）时才顺手捕猎，体现"幼崽也能捕猎"。
	if a.Age < a.Sp.Reproduce.MatureAge {
		if s.followAdult(w, c, a) {
			return
		}
		// 无成年体可跟随：若眼前就有易捕的鹿则顺手捕，否则原地等待不乱窜
		if prey := s.nearest(c, a, ambush, "deer"); prey != nil {
			if a.HuntCool <= 0 {
				s.hunt(w, c, a, prey, r)
			}
		}
		return
	}

	// 范围内有鹿（易捕）且未吃满：捕猎（即使不饿也吃满饱食度）
	if prey := s.nearest(c, a, ambush, "deer"); prey != nil {
		if needFood && a.HuntCool <= 0 {
			s.hunt(w, c, a, prey, r)
		}
		return
	}
	// 视野内有近岸鹿（冲刺范围内）：上岸扑击
	if prey := s.nearest(c, a, a.Sp.Vision, "deer"); prey != nil {
		strike := a.Sp.StrikeRange
		if strike <= 0 {
			strike = 1
		}
		if needFood && chebyshev(a.X, a.Y, prey.X, prey.Y) <= ambush+strike {
			s.crocStrike(w, c, a, prey, r)
			return
		}
		// 未吃满则沿河靠近猎物
		if needFood {
			s.approachInWater(w, c, a, prey)
			return
		}
	}

	// 未吃满：无近岸猎物则食腐或沿河巡游
	if needFood {
		if corpse := s.nearestCorpse(w, c, a, a.Sp.Vision); corpse != nil {
			s.scavenge(w, c, a)
			return
		}
		s.crocPatrol(w, c, a, r)
		return
	}

	// 已吃满：成体饱腹，尝试产蛋或待机
	if a.EggCool <= 0 && a.Sp.Egg.Cooldown > 0 {
		if s.layEgg(w, c, a, r) {
			return
		}
	}
	s.crocIdle(w, c, a, r)
}

// layEgg 饱腹鳄鱼到最近陆地格产一颗蛋，返回是否成功。
// 优先选择"紧邻河流"的岸边陆地格，保证孵化出的幼崽能立即进入附近水域。
func (s *Behave) layEgg(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) bool {
	// 繁殖需处于青壮年：超过繁殖年龄上限则不再产蛋
	if a.Sp.Reproduce.MaxBreedAge > 0 && a.Age > a.Sp.Reproduce.MaxBreedAge {
		return false
	}
	// 第一轮：找紧邻河流的岸边陆地格（周围 8 邻域有水）
	x, y := -1, -1
	best := 1 << 30
	pick := func(bank bool) {
		for dy := -a.Sp.Vision; dy <= a.Sp.Vision; dy++ {
			for dx := -a.Sp.Vision; dx <= a.Sp.Vision; dx++ {
				nx, ny := a.X+dx, a.Y+dy
				if !w.Grid.InBounds(nx, ny) || w.Grid.IsRiver(nx, ny) {
					continue
				}
				if s.occupiedByOther(c, nx, ny, a.ID) {
					continue
				}
				// bank=true 时只选紧邻河岸的陆地
				if bank && !w.Grid.NearRiver(nx, ny, 1) {
					continue
				}
				dd := chebyshev(nx, ny, a.X, a.Y)
				if dd < best {
					best, x, y = dd, nx, ny
				}
			}
		}
	}
	pick(true)
	if x < 0 {
		// 无岸边空位则退而选任意陆地空位
		best = 1 << 30
		pick(false)
	}
	if x < 0 {
		return false // 附近无可用陆地
	}
	// 孵化时间在 [hatch_min, hatch_max] 间波动
	hatch := a.Sp.Egg.HatchMin
	if a.Sp.Egg.HatchMax > a.Sp.Egg.HatchMin {
		hatch += r.Intn(a.Sp.Egg.HatchMax - a.Sp.Egg.HatchMin + 1)
	}
	// 产蛋消耗：母体把 child_energy 转移进蛋（能量守恒：动物域 → 蛋域）
	eggEnergy := a.Sp.Reproduce.ChildEnergy
	if eggEnergy <= 0 {
		eggEnergy = 20
	}
	cost := a.Sp.Reproduce.Cost
	if cost > a.Energy {
		return false // 能量不足，无法产蛋
	}
	a.Energy -= cost
	c.Ledger.Add(a.Species+".egg", -cost)
	w.AddEgg(a.Species, x, y, hatch, eggEnergy)
	a.EggCool = a.Sp.Egg.Cooldown
	c.Ev.Emit(w.Tick, "lay_egg", a.ID, 0, 0)
	return true
}

// approachInWater 鳄鱼沿河道向猎物靠近 1 格：只在自身可进入的水格中选，
// 挑切比雪夫距离最小的格子，全程不离开水域。
func (s *Behave) approachInWater(w *world.World, c *core.Ctx, a *world.Animal, prey *world.Animal) {
	cur := chebyshev(a.X, a.Y, prey.X, prey.Y)
	bestX, bestY := -1, -1
	bestDist := cur
	for _, d := range directions {
		nx, ny := a.X+d[0], a.Y+d[1]
		if !w.CanEnter(nx, ny, a.Sp) {
			continue // 不能离开水域
		}
		nd := chebyshev(nx, ny, prey.X, prey.Y)
		if nd < bestDist {
			bestDist, bestX, bestY = nd, nx, ny
		}
	}
	if bestX < 0 {
		return // 无更靠近的水格（如被河道走向限制），原地待机
	}
	s.move(w, c, a, bestX-a.X, bestY-a.Y)
}

// crocStrike 鳄鱼上岸突袭：从水中朝猎物方向冲刺（最多 strike_range 格，允许跨上陆地扑击），
// 冲刺后若已进入 kill_radius 立即捕杀；无论是否得手，本 tick 内随即回到最近水格，
// 保证"冲上岸→咬→回水"一气呵成，绝不滞留陆地。
// 仅在水中发起——避免在岸上持续追击深入陆地。
func (s *Behave) crocStrike(w *world.World, c *core.Ctx, a *world.Animal, prey *world.Animal, r *rng.Rng) {
	if !w.Grid.IsRiver(a.X, a.Y) {
		return // 不在水中，不发起新的突袭（由主逻辑处理回水）
	}
	strike := a.Sp.StrikeRange
	if strike <= 0 {
		strike = 1
	}
	killR := a.Sp.KillRadius
	if killR <= 0 {
		killR = 1
	}
	cost := c.Params.At(a.Species+".move_cost", a.X, a.Y)
	for i := 0; i < strike; i++ {
		// 一旦贴近猎物就停止冲刺并捕杀
		if chebyshev(a.X, a.Y, prey.X, prey.Y) <= killR {
			break
		}
		dx, dy := sign(prey.X-a.X), sign(prey.Y-a.Y)
		if dx == 0 && dy == 0 {
			break
		}
		nx, ny := a.X+dx, a.Y+dy
		if !w.Grid.InBounds(nx, ny) {
			break
		}
		// 突袭移动：绕过白名单限制（允许上岸），但仍扣移动成本
		a.X, a.Y = nx, ny
		a.Energy -= cost
		a.Moved = true
		c.Ledger.Add(a.Species+".move", -cost)
	}
	// 冲刺结束后若已贴近猎物，立即捕杀
	if chebyshev(a.X, a.Y, prey.X, prey.Y) <= killR && a.HuntCool <= 0 {
		s.hunt(w, c, a, prey, r)
	}
	// 本 tick 内立即回水，杜绝滞留陆地（bug 修复）
	if !w.Grid.IsRiver(a.X, a.Y) {
		s.returnToWater(w, c, a)
	}
}

// returnToWater 鳄鱼从岸上回到最近的水格（切比雪夫距离最近的河流格），恢复水域伏击姿态。
// 搜索范围逐步扩大到 strike_range+1，确保深入陆地突袭后仍能找到回家的水格（修复卡岸 bug）。
func (s *Behave) returnToWater(w *world.World, c *core.Ctx, a *world.Animal) {
	maxR := a.Sp.StrikeRange + 1
	if maxR < 2 {
		maxR = 2
	}
	bestX, bestY := -1, -1
	bestDist := 1 << 30
	for r := 1; r <= maxR; r++ {
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				nx, ny := a.X+dx, a.Y+dy
				if !w.Grid.IsRiver(nx, ny) {
					continue
				}
				dd := chebyshev(nx, ny, a.X, a.Y)
				if dd < bestDist {
					bestDist, bestX, bestY = dd, nx, ny
				}
			}
		}
		if bestX >= 0 {
			break // 已找到最近水格
		}
	}
	if bestX < 0 {
		return // 周围无水域（异常），原地
	}
	cost := c.Params.At(a.Species+".move_cost", a.X, a.Y)
	a.X, a.Y = bestX, bestY
	a.Energy -= cost
	a.Moved = true
	c.Ledger.Add(a.Species+".move", -cost)
}

// crocPatrol 鳄鱼饥饿巡游：沿河移动 1 格（只在水中），主动寻找猎物；刚需，不受 move_chance 限制。
func (s *Behave) crocPatrol(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) {
	start := r.Intn(len(directions))
	for i := 0; i < len(directions); i++ {
		d := directions[(start+i)%len(directions)]
		if s.move(w, c, a, d[0], d[1]) {
			return
		}
	}
	// 所有方向都不可进入（罕见），原地
}

// crocIdle 鳄鱼待机：按 move_chance 决定是否在水中随机挪动 1 格，否则静止（低代谢伏击）。
func (s *Behave) crocIdle(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) {
	if !s.rollMove(c, a, r) {
		return // 静止：代谢按 idle_metabolism_mult 打折
	}
	// 在水中随机挪动（移出后方向若不可进入则放弃本次移动）
	start := r.Intn(len(directions))
	for i := 0; i < len(directions); i++ {
		d := directions[(start+i)%len(directions)]
		if s.move(w, c, a, d[0], d[1]) {
			return
		}
	}
}

// rollMove 按物种 move_chance 判定本 tick 是否允许移动（rng 消耗固定为 1 次，
// 保证行为确定性）。move_chance<=0 时视为总是允许（兼容未配置的物种）。
func (s *Behave) rollMove(c *core.Ctx, a *world.Animal, r *rng.Rng) bool {
	chance := c.Params.At(a.Species+".move_chance", a.X, a.Y)
	if chance <= 0 {
		return true
	}
	if chance >= 1 {
		return true
	}
	return r.Float64() < chance
}

// juvenileTiger 幼虎行为：饥饿时优先吃尸体/捕猎，饱腹时跟随最近成年同类。
func (s *Behave) juvenileTiger(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) bool {
	if s.isHungry(c, a) {
		if s.scavenge(w, c, a) {
			return true
		}
		s.huntNearest(w, c, a, r)
		return true
	}
	return s.followAdult(w, c, a)
}

// provideForYoung 饱腹成年虎：附近有幼崽且无新鲜尸体时，捕猎供幼崽食用。
func (s *Behave) provideForYoung(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) bool {
	vision := a.Sp.Vision
	hasYoung := s.hasNearbyJuvenile(c, a, vision)
	if !hasYoung {
		return false
	}
	// 附近已有可食用的尸体则无需捕猎
	if s.nearestCorpse(w, c, a, vision) != nil {
		return false
	}
	s.huntNearest(w, c, a, r)
	return true
}

// search 在视野内无猎物时扩大搜索半径寻找更远的鹿，朝其方向移动，
// 避免纯随机游走在开阔网格中找不到下一只猎物而饿死。
func (s *Behave) search(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) {
	vision := a.Sp.Vision
	if prey := s.nearest(c, a, vision*2, "deer"); prey != nil {
		dx, dy := sign(prey.X-a.X), sign(prey.Y-a.Y)
		s.move(w, c, a, dx, dy)
		return
	}
	s.wander(w, c, a, r)
}

// huntOrScavenge 饥饿时觅食：附近有新鲜尸体就吃（食腐省力，优先），否则追捕活鹿。
func (s *Behave) huntOrScavenge(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) bool {
	// 食腐优先：有现成尸体就吃，省去追捕的体力
	if s.scavenge(w, c, a) {
		return true
	}
	if s.nearest(c, a, a.Sp.Vision, "deer") != nil {
		s.huntNearest(w, c, a, r)
		return true
	}
	s.search(w, c, a, r)
	return true
}

// huntNearest 找视野内最近的鹿并追猎（冲刺 2 格，进入 kill_radius 即捕猎）；无鹿则扩大搜索。
func (s *Behave) huntNearest(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) {
	vision := a.Sp.Vision
	// 进入 kill_radius 即可捕猎（不必贴到相邻），与"太近无条件捕杀"一致
	killR := a.Sp.KillRadius
	if killR <= 0 {
		killR = 1
	}
	prey := s.nearest(c, a, vision, "deer")
	if prey == nil {
		s.search(w, c, a, r)
		return
	}
	if chebyshev(a.X, a.Y, prey.X, prey.Y) <= killR {
		s.hunt(w, c, a, prey, r)
		return
	}
	dx, dy := sign(prey.X-a.X), sign(prey.Y-a.Y)
	for i := 0; i < 2 && chebyshev(a.X, a.Y, prey.X, prey.Y) > killR; i++ {
		if !s.move(w, c, a, dx, dy) {
			break
		}
	}
	if chebyshev(a.X, a.Y, prey.X, prey.Y) <= killR {
		s.hunt(w, c, a, prey, r)
	}
}

// isJuvenile 判断是否为幼崽（未达成熟年龄）。
func (s *Behave) isJuvenile(c *core.Ctx, a *world.Animal) bool {
	return a.Age < a.Sp.Reproduce.MatureAge
}

// isHungry 判断是否处于饥饿状态（能量低于饥饿阈值，对应饱食惰性的解除点）。
func (s *Behave) isHungry(c *core.Ctx, a *world.Animal) bool {
	cap := energyCap(c, a)
	hungry := a.Sp.SatiationHungry
	return hungry <= 0 || a.Energy <= cap*hungry
}

// occupiedByOther 判断某格是否已被除 selfID 外的其他活体动物占用。
// 用半径 0 的空间查询实现，只扫描目标格所在分桶，开销极低。
func (s *Behave) occupiedByOther(c *core.Ctx, x, y, selfID int) bool {
	if c.Index == nil {
		return false
	}
	for _, o := range c.Index.AnimalsInRadius(x, y, 0, "") {
		if !o.Dead && o.ID != selfID {
			return true
		}
	}
	return false
}

// followAdult 幼崽跟随视野内最近的成年同类：每 tick 朝其移动 1 格，目标是停在
// 距离成年体 1~2 格的空格上（松散跟随）。一旦进入 1~2 格范围且当前格未被挤占，
// 即视为"已到达"，原地不动——避免每 tick 随机期望距离导致幼崽在 1~2 格间来回震荡。
// 选格规则：优先未被其他动物占用、且不与成年体重叠，按"贴近 1~2 格范围"评分取最优；
// 若周围格子都被占满（格子不够），则原地不动、容忍重叠。
func (s *Behave) followAdult(w *world.World, c *core.Ctx, a *world.Animal) bool {
	vision := a.Sp.Vision
	mature := a.Sp.Reproduce.MatureAge
	adult := s.nearestAdult(c, a, vision, mature)
	if adult == nil {
		return false // 视野内无成年同类，原地不动
	}
	dist := chebyshev(a.X, a.Y, adult.X, adult.Y)
	// 已进入跟随范围（1~2 格）且当前格没被其他动物挤占，就保持不动（已到达）
	if dist >= 1 && dist <= 2 && !s.occupiedByOther(c, a.X, a.Y, a.ID) {
		return false
	}
	// 朝成年体移动 1 格：在 8 方向中挑一个"可进入 + 未被占用 + 不与成年体重叠"的格子，
	// 评分 = 目标格到成年体的距离，越小越优（朝 1~2 格跟随范围靠近）
	bestX, bestY := -1, -1
	bestScore := 1 << 30
	// 退让候选：当前已与成年体同格时，若周围无完全空闲格，则允许落到"被其他幼崽占用
	// 但能脱离成年体"的格子——优先保证不与成年体重叠（格子不够时的次优解）。
	fallbackX, fallbackY := -1, -1
	fallbackScore := 1 << 30
	for _, d := range directions {
		nx, ny := a.X+d[0], a.Y+d[1]
		if !w.CanEnter(nx, ny, a.Sp) {
			continue
		}
		nd := chebyshev(nx, ny, adult.X, adult.Y)
		if nd == 0 {
			continue // 不与成年体站在同一格
		}
		// 评分：优先落在 1~2 格范围内（范围内记 0 代价），范围外按距离惩罚
		score := nd
		if nd >= 1 && nd <= 2 {
			score = 0
		}
		if s.occupiedByOther(c, nx, ny, a.ID) {
			// 被占用：仅当当前与成年体同格时作为退让候选
			if dist == 0 && score < fallbackScore {
				fallbackScore, fallbackX, fallbackY = score, nx, ny
			}
			continue // 避开已被其他动物占用的格子
		}
		if score < bestScore {
			bestScore, bestX, bestY = score, nx, ny
		}
	}
	if bestX < 0 {
		if fallbackX >= 0 {
			s.move(w, c, a, fallbackX-a.X, fallbackY-a.Y)
			return true
		}
		return false // 周围无可用空格（格子不够），容忍重叠，原地不动
	}
	s.move(w, c, a, bestX-a.X, bestY-a.Y)
	return true
}

// nearestAdult 找视野内最近的成年同类。
func (s *Behave) nearestAdult(c *core.Ctx, a *world.Animal, radius, mature int) *world.Animal {
	if c.Index == nil {
		return nil
	}
	list := c.Index.AnimalsInRadius(a.X, a.Y, radius, a.Species)
	var best *world.Animal
	bestDist := 0
	for _, candidate := range list {
		if candidate.Dead || candidate.ID == a.ID || candidate.Age < mature {
			continue
		}
		dist := chebyshev(a.X, a.Y, candidate.X, candidate.Y)
		if dist > radius {
			continue
		}
		if best == nil || dist < bestDist || (dist == bestDist && candidate.ID < best.ID) {
			best, bestDist = candidate, dist
		}
	}
	return best
}

// hasNearbyJuvenile 判断视野内是否有幼年同类。
func (s *Behave) hasNearbyJuvenile(c *core.Ctx, a *world.Animal, radius int) bool {
	mature := a.Sp.Reproduce.MatureAge
	if c.Index == nil {
		return false
	}
	for _, candidate := range c.Index.AnimalsInRadius(a.X, a.Y, radius, a.Species) {
		if !candidate.Dead && candidate.ID != a.ID && candidate.Age < mature {
			return true
		}
	}
	return false
}

// nearestCorpse 找视野内最近的新鲜非同类尸体（可食用的）。
func (s *Behave) nearestCorpse(w *world.World, c *core.Ctx, a *world.Animal, radius int) *world.Corpse {
	minFresh := a.Sp.ScavengeFreshness
	if minFresh <= 0 {
		return nil // 该物种不食腐
	}
	var best *world.Corpse
	bestDist := 0
	for _, corpse := range w.Corpses {
		if corpse.Species == a.Species || corpse.Remaining <= 1e-12 {
			continue // 同类不相食 / 已空
		}
		if corpse.Freshness() < minFresh {
			continue // 不够新鲜
		}
		dist := chebyshev(a.X, a.Y, corpse.X, corpse.Y)
		if dist > radius {
			continue
		}
		if best == nil || dist < bestDist || (dist == bestDist && corpse.ID < best.ID) {
			best, bestDist = corpse, dist
		}
	}
	return best
}

// scavenge 吃视野内最近的新鲜非同类尸体，恢复能量。
func (s *Behave) scavenge(w *world.World, c *core.Ctx, a *world.Animal) bool {
	vision := a.Sp.Vision
	corpse := s.nearestCorpse(w, c, a, vision)
	if corpse == nil {
		return false
	}
	if chebyshev(a.X, a.Y, corpse.X, corpse.Y) <= 1 {
		cap := energyCap(c, a)
		rate := c.Params.Get("global.scavenge_rate")
		if rate <= 0 {
			rate = 20
		}
		eaten := math.Min(rate, corpse.Remaining)
		gain := eaten * c.Params.Get("global.scavenge_efficiency")
		room := cap - a.Energy
		if gain > room {
			gain = room
			eaten = gain / c.Params.Get("global.scavenge_efficiency")
		}
		if eaten <= 0 {
			return false
		}
		corpse.Remaining -= eaten
		a.Energy += gain
		c.Ledger.Add("corpse.eaten", gain)
		c.Metrics.Scavenge()
		c.Ev.Emit(w.Tick, "scavenge", a.ID, corpse.ID, gain)
		return true
	}
	// 朝尸体移动
	dx, dy := sign(corpse.X-a.X), sign(corpse.Y-a.Y)
	s.move(w, c, a, dx, dy)
	return true
}

func (s *Behave) eat(w *world.World, c *core.Ctx, a *world.Animal) bool {
	i := w.Grid.Idx(a.X, a.Y)
	rate := a.Sp.Diet.Rate
	cap := energyCap(c, a)
	if a.Satiated || w.Grid.Grass[i] < rate || a.Energy >= cap {
		return false
	}
	efficiency := a.Sp.Diet.Efficiency
	eaten := math.Min(rate, w.Grid.Grass[i])
	gain := eaten * efficiency
	room := cap - a.Energy
	if gain > room {
		gain = room
		eaten = gain / efficiency
	}
	if eaten <= 0 {
		return false
	}
	w.Grid.Grass[i] -= eaten
	a.Energy += gain
	w.Grid.Nutrient[i] += a.Sp.Diet.DungNutrient
	if nutrientCap := c.Params.Get("grass.nutrient_cap"); nutrientCap > 0 && w.Grid.Nutrient[i] > nutrientCap {
		w.Grid.Nutrient[i] = nutrientCap
	}
	c.Ledger.AddTransfer("grass.deer", gain)
	c.Ledger.Add("grass.dissip", -(eaten - gain))
	return true
}

func (s *Behave) forage(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) {
	vision := a.Sp.Vision
	// 保命优先：选觅食点时避开距离虎 ≤ avoid_radius 的格子，
	// 即使那里草最多也不去（草在虎那边就不过去）。
	avoid := a.Sp.AvoidRadius
	threats := c.Index.AnimalsInRadius(a.X, a.Y, vision+avoid, "tiger")
	isNearTiger := func(x, y int) bool {
		for _, t := range threats {
			if t.Dead {
				continue
			}
			if chebyshev(x, y, t.X, t.Y) <= avoid {
				return true
			}
		}
		return false
	}

	bestX, bestY := -1, -1
	bestGrass := 0.0
	bestDist := 0
	for y := a.Y - vision; y <= a.Y+vision; y++ {
		for x := a.X - vision; x <= a.X+vision; x++ {
			if !w.Grid.InBounds(x, y) {
				continue
			}
			if isNearTiger(x, y) {
				continue // 靠近虎的格子直接排除
			}
			dist := chebyshev(a.X, a.Y, x, y)
			grass := w.Grid.Grass[w.Grid.Idx(x, y)]
			if grass > bestGrass || (grass == bestGrass && bestX >= 0 && dist < bestDist) {
				bestX, bestY, bestGrass, bestDist = x, y, grass, dist
			}
		}
	}
	if bestX < 0 || (bestX == a.X && bestY == a.Y) {
		s.wander(w, c, a, r)
		return
	}
	dx, dy := sign(bestX-a.X), sign(bestY-a.Y)
	s.move(w, c, a, dx, dy)
}

func (s *Behave) tryReproduce(w *world.World, c *core.Ctx, a *world.Animal) bool {
	sp := a.Sp
	threshold := sp.Reproduce.EnergyThreshold
	mature := sp.Reproduce.MatureAge
	// 繁殖需处于青壮年：达到成熟年龄、且未超过繁殖年龄上限（max_breed_age>0 时）
	if a.Energy < threshold || a.Cooldown != 0 || a.Age < mature {
		return false
	}
	if sp.Reproduce.MaxBreedAge > 0 && a.Age > sp.Reproduce.MaxBreedAge {
		return false
	}
	cost := sp.Reproduce.Cost
	childEnergy := sp.Reproduce.ChildEnergy
	a.Energy -= cost
	// 哺乳：母体把自身剩余能量的一部分转移给幼崽，作为出生后的缓冲
	nurse := a.Energy * sp.Reproduce.NurseRatio
	a.Energy -= nurse
	a.Cooldown = sp.Reproduce.Cooldown
	childID := w.NextID
	lifespanR := rng.New(w.Seed, rng.StreamLifespan, w.Tick, childID)
	child := w.AddAnimal(a.Species, sp, a.X, a.Y, childEnergy+nurse, lifespanR.IntRange(sp.Lifespan[0], sp.Lifespan[1]))
	c.Ledger.Add(a.Species+".reproduce", childEnergy-cost)
	c.Metrics.Birth()
	c.Ev.Emit(w.Tick, "born", child.ID, a.ID, childEnergy+nurse)
	return true
}

// hunt 捕食者捕杀猎物（物种无关：参数与账本 key 都按捕食者物种动态取，
// 因此虎仍是 tiger.*、鳄鱼是 crocodile.*，互不干扰）。
func (s *Behave) hunt(w *world.World, c *core.Ctx, predator, prey *world.Animal, r *rng.Rng) {
	pname := predator.Species
	success := r.Chance(c.Params.At(pname+".hunt_success", predator.X, predator.Y))
	if success {
		cap := energyCap(c, predator)
		gain := math.Min(predator.Sp.Hunt.Gain, math.Max(0, cap-predator.Energy))
		predator.Energy += gain
		prey.Dead = true
		prey.Cause = "predated"
		sp := c.Cfg.Species[prey.Species]
		// 尸体量 = 猎物被吃后剩余的能量（捕食者吃光了则无尸体）
		remain := math.Max(0, prey.Energy-gain)
		w.AddCorpse(prey.Species, prey.X, prey.Y, remain, sp.Corpse.Ticks)
		// 捕杀后进入冷却，避免连续屠戮周围猎物
		predator.HuntCool = predator.Sp.HuntCooldown
		c.Ledger.AddTransfer(prey.Species+"."+pname, gain)
		c.Ledger.Add("corpse.residual", -(prey.Energy - gain))
		c.Metrics.Predated()
		c.Ev.Emit(w.Tick, "hunt", predator.ID, prey.ID, gain)
		c.Ev.Emit(w.Tick, "died", prey.ID, predator.ID, deathCode("predated"))
		return
	}

	predCost := predator.Sp.Hunt.FailTigerCost
	preyCost := predator.Sp.Hunt.FailDeerCost
	predator.Energy -= predCost
	prey.Energy -= preyCost
	c.Ledger.Add(pname+".hunt_fail", -predCost)
	c.Ledger.Add(prey.Species+".hunt_fail", -preyCost)
	preyR := rng.New(w.Seed, rng.StreamBehavior, w.Tick, prey.ID)
	dir := directions[preyR.Intn(len(directions))]
	jump := predator.Sp.Hunt.FleeJump
	for i := 0; i < jump; i++ {
		nx, ny := prey.X+dir[0], prey.Y+dir[1]
		// 逃窜不能穿过不可穿越地形（如河流），碰到即停
		if !w.CanEnter(nx, ny, prey.Sp) {
			break
		}
		prey.X, prey.Y = nx, ny
	}
	c.Ev.Emit(w.Tick, "hunt", predator.ID, prey.ID, 0)
}

func (s *Behave) wander(w *world.World, c *core.Ctx, a *world.Animal, r *rng.Rng) {
	if r.Intn(2) == 0 {
		return // 50% 概率原地不动（§9.3 WANDER）
	}
	d := directions[r.Intn(len(directions))]
	s.move(w, c, a, d[0], d[1])
}

func (s *Behave) move(w *world.World, c *core.Ctx, a *world.Animal, dx, dy int) bool {
	nx, ny := a.X+dx, a.Y+dy
	// 越界或落在该物种不可穿越的地形上（如鹿不能过河）则原地不动
	if !w.CanEnter(nx, ny, a.Sp) {
		return false
	}
	cost := c.Params.At(a.Species+".move_cost", a.X, a.Y)
	a.X, a.Y = nx, ny
	a.Energy -= cost
	a.Moved = true // 标记本 tick 移动过（静止代谢判定）
	c.Ledger.Add(a.Species+".move", -cost)
	return true
}

func (s *Behave) nearest(c *core.Ctx, a *world.Animal, radius int, species string) *world.Animal {
	if c.Index == nil {
		return nil
	}
	list := c.Index.AnimalsInRadius(a.X, a.Y, radius, species)
	var best *world.Animal
	bestDist := 0
	for _, candidate := range list {
		if candidate.Dead {
			continue
		}
		dist := chebyshev(a.X, a.Y, candidate.X, candidate.Y)
		if dist > radius {
			continue
		}
		if best == nil || dist < bestDist || (dist == bestDist && candidate.ID < best.ID) {
			best, bestDist = candidate, dist
		}
	}
	return best
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	default:
		return 0
	}
}



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
		r := rng.New(w.Seed, rng.StreamBehavior, w.Tick, a.ID)
		if a.Species == "deer" {
			s.deer(w, c, a, r)
		} else if a.Species == "tiger" {
			s.tiger(w, c, a, r)
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

// followAdult 幼崽跟随视野内最近的成年同类；无成年同类则原地不动。
func (s *Behave) followAdult(w *world.World, c *core.Ctx, a *world.Animal) bool {
	vision := a.Sp.Vision
	mature := a.Sp.Reproduce.MatureAge
	adult := s.nearestAdult(c, a, vision, mature)
	if adult == nil {
		return false // 视野内无成年同类，原地不动
	}
	dx, dy := sign(adult.X-a.X), sign(adult.Y-a.Y)
	if dx == 0 && dy == 0 {
		return false // 已在成年同类旁
	}
	s.move(w, c, a, dx, dy)
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
	if a.Energy < threshold || a.Cooldown != 0 || a.Age < mature {
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

func (s *Behave) hunt(w *world.World, c *core.Ctx, tiger, deer *world.Animal, r *rng.Rng) {
	success := r.Chance(c.Params.At("tiger.hunt_success", tiger.X, tiger.Y))
	if success {
		cap := energyCap(c, tiger)
		gain := math.Min(tiger.Sp.Hunt.Gain, math.Max(0, cap-tiger.Energy))
		tiger.Energy += gain
		deer.Dead = true
		deer.Cause = "predated"
		sp := c.Cfg.Species[deer.Species]
		// 尸体量 = 鹿被吃后剩余的能量（虎吃光了则无尸体）
		remain := math.Max(0, deer.Energy-gain)
		w.AddCorpse(deer.Species, deer.X, deer.Y, remain, sp.Corpse.Ticks)
		// 捕杀后进入冷却，避免连续屠戮周围鹿
		tiger.HuntCool = tiger.Sp.HuntCooldown
		c.Ledger.AddTransfer("deer.tiger", gain)
		c.Ledger.Add("corpse.residual", -(deer.Energy - gain))
		c.Metrics.Predated()
		c.Ev.Emit(w.Tick, "hunt", tiger.ID, deer.ID, gain)
		c.Ev.Emit(w.Tick, "died", deer.ID, tiger.ID, deathCode("predated"))
		return
	}

	tigerCost := tiger.Sp.Hunt.FailTigerCost
	deerCost := tiger.Sp.Hunt.FailDeerCost
	tiger.Energy -= tigerCost
	deer.Energy -= deerCost
	c.Ledger.Add("tiger.hunt_fail", -tigerCost)
	c.Ledger.Add("deer.hunt_fail", -deerCost)
	deerR := rng.New(w.Seed, rng.StreamBehavior, w.Tick, deer.ID)
	dir := directions[deerR.Intn(len(directions))]
	jump := tiger.Sp.Hunt.FleeJump
	for i := 0; i < jump; i++ {
		nx, ny := deer.X+dir[0], deer.Y+dir[1]
		// 逃窜不能穿过不可穿越地形（如河流），碰到即停
		if !w.CanEnter(nx, ny, deer.Sp) {
			break
		}
		deer.X, deer.Y = nx, ny
	}
	c.Ev.Emit(w.Tick, "hunt", tiger.ID, deer.ID, 0)
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



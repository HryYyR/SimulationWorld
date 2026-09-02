package world

import (
	"math"

	"ecosim/internal/config"
	"ecosim/internal/env"
	"ecosim/internal/rng"
)

type Grid struct {
	W, H     int
	Grass    []float64
	Nutrient []float64
}

func (g *Grid) Idx(x, y int) int { return y*g.W + x }

func (g *Grid) InBounds(x, y int) bool {
	return x >= 0 && y >= 0 && x < g.W && y < g.H
}

type Animal struct {
	ID       int
	Species  string
	X, Y     int
	Age      int
	Energy   float64
	Lifespan int
	Cooldown int
	Dead     bool
	Cause    string
	Satiated bool // 饱食惰性：吃饱后停止进食/捕猎，降到饥饿阈值以下才恢复
	HuntCool int // 捕杀冷却剩余 tick：捕杀后需休息，避免连续屠戮周围鹿
	Sp       *config.Species // 物种静态参数缓存（避免每 tick 字符串拼接查表）
}

type Corpse struct {
	ID         int
	Species    string
	X, Y       int
	Total      float64
	Remaining  float64
	TotalTicks int
	TicksLeft  int
}

// Freshness 返回尸体新鲜度（0~1），随腐烂时间线性下降。
func (c *Corpse) Freshness() float64 {
	if c.TotalTicks <= 0 {
		return 0
	}
	f := float64(c.TicksLeft) / float64(c.TotalTicks)
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

type World struct {
	RulesVersion int
	Tick         int
	Seed         uint64
	Grid         Grid
	Animals      []*Animal
	Corpses      []*Corpse
	byID         map[int]*Animal
	NextID       int
	Weather      env.WeatherState
}

func Gen(cfg *config.Root, seed uint64) *World {
	w := &World{
		RulesVersion: cfg.Balance.RulesVersion,
		Seed:         seed,
		Grid: Grid{
			W:        cfg.Balance.World.Width,
			H:        cfg.Balance.World.Height,
			Grass:    make([]float64, cfg.Balance.World.Width*cfg.Balance.World.Height),
			Nutrient: make([]float64, cfg.Balance.World.Width*cfg.Balance.World.Height),
		},
		byID:   make(map[int]*Animal),
		NextID: 1,
	}
	r := rng.New(seed, rng.StreamWorldGen, 0, 0)
	noise := cfg.Balance.Init.Noise
	for i := range w.Grid.Grass {
		w.Grid.Grass[i] = clamp(cfg.Balance.Init.Grass+(r.Float64()*2-1)*noise, 0, 100)
		w.Grid.Nutrient[i] = clamp(cfg.Balance.Init.Nutrient+(r.Float64()*2-1)*noise, 0, 100)
	}
	occupied := make(map[[2]int]bool)
	spawn := func(species string, x, y int, energy float64) *Animal {
		sp := cfg.Species[species]
		// 在目标位置附近找一个空位
		for dx := -2; dx <= 2; dx++ {
			for dy := -2; dy <= 2; dy++ {
				nx, ny := x+dx, y+dy
				if nx < 0 || nx >= w.Grid.W || ny < 0 || ny >= w.Grid.H {
					continue
				}
				if occupied[[2]int{nx, ny}] {
					continue
				}
				occupied[[2]int{nx, ny}] = true
				return w.AddAnimal(species, &sp, nx, ny, energy, rng.New(seed, rng.StreamLifespan, 0, w.NextID).IntRange(sp.Lifespan[0], sp.Lifespan[1]))
			}
		}
		return nil
	}
	// 族群初始化：每个族群由若干成年个体 + 幼崽组成，聚集在随机中心附近。
	// 族群中心按"抖动网格"均匀分布，避免多个族群随机挤在地图一角导致局部猎物枯竭。
	spawnPack := func(species string, packs, adults, juveniles int, adultEnergy, childEnergy float64) {
		sp := cfg.Species[species]
		gridCols := int(math.Sqrt(float64(packs)))
		if gridCols < 1 {
			gridCols = 1
		}
		gridRows := (packs + gridCols - 1) / gridCols
		cellW := w.Grid.W / gridCols
		cellH := w.Grid.H / gridRows
		if cellW < 1 {
			cellW = 1
		}
		if cellH < 1 {
			cellH = 1
		}
		for p := 0; p < packs; p++ {
			gx := p % gridCols
			gy := p / gridCols
			// 在所属网格单元内随机取中心，保证族群均匀铺满地图
			cx := gx*cellW + r.Intn(cellW)
			cy := gy*cellH + r.Intn(cellH)
			if cx >= w.Grid.W {
				cx = w.Grid.W - 1
			}
			if cy >= w.Grid.H {
				cy = w.Grid.H - 1
			}
			// 成年个体：成熟可繁殖，能量接近满额
			for i := 0; i < adults; i++ {
				if a := spawn(species, cx, cy, adultEnergy); a != nil {
					a.Age = sp.Reproduce.MatureAge
				}
			}
			// 幼崽：初始能量为 child_energy，钳制到幼年上限
			for i := 0; i < juveniles; i++ {
				energy := childEnergy
				if m := sp.JuvenileEnergyCapMult; m > 0 {
					if cap := cfg.Balance.EnergyCap * m; energy > cap {
						energy = cap
					}
				}
				spawn(species, cx, cy, energy)
			}
		}
	}
	spawnPack("deer", cfg.Balance.Init.DeerPacks, cfg.Balance.Init.PackAdults, cfg.Balance.Init.PackJuveniles, cfg.Balance.EnergyCap, cfg.Species["deer"].Reproduce.ChildEnergy)
	spawnPack("tiger", cfg.Balance.Init.TigerPacks, cfg.Balance.Init.PackAdults, cfg.Balance.Init.PackJuveniles, cfg.Balance.EnergyCap, cfg.Species["tiger"].Reproduce.ChildEnergy)
	w.Weather = env.InitialWeather(cfg, r)
	return w
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (w *World) AddAnimal(species string, sp *config.Species, x, y int, energy float64, lifespan int) *Animal {
	a := &Animal{ID: w.NextID, Species: species, X: x, Y: y, Energy: energy, Lifespan: lifespan, Sp: sp}
	w.NextID++
	w.Animals = append(w.Animals, a)
	w.byID[a.ID] = a
	return a
}

func (w *World) Animal(id int) *Animal { return w.byID[id] }

func (w *World) RemoveAnimal(id int) bool {
	a, ok := w.byID[id]
	if !ok {
		return false
	}
	delete(w.byID, id)
	for i, v := range w.Animals {
		if v == a {
			w.Animals = append(w.Animals[:i], w.Animals[i+1:]...)
			break
		}
	}
	return true
}

func (w *World) RebuildIndex() {
	w.byID = make(map[int]*Animal, len(w.Animals))
	for _, a := range w.Animals {
		w.byID[a.ID] = a
	}
}

func (w *World) AddCorpse(species string, x, y int, total float64, ticks int) *Corpse {
	c := &Corpse{
		ID: w.NextID, Species: species, X: x, Y: y,
		Total: total, Remaining: total, TotalTicks: ticks, TicksLeft: ticks,
	}
	w.NextID++
	w.Corpses = append(w.Corpses, c)
	return c
}

type SpatialIndex interface {
	AnimalsInRadius(x, y, r int, species string) []*Animal
}

// NaiveIndex 暴力遍历所有动物，实现简单但 O(n)。保留作为回退。
type NaiveIndex struct {
	animals []*Animal
}

func NewNaiveIndex(animals []*Animal) *NaiveIndex {
	return &NaiveIndex{animals: animals}
}

func (n *NaiveIndex) AnimalsInRadius(x, y, r int, species string) []*Animal {
	out := make([]*Animal, 0)
	for _, a := range n.animals {
		if a.Dead || (species != "" && a.Species != species) {
			continue
		}
		if chebyshev(x, y, a.X, a.Y) <= r {
			out = append(out, a)
		}
	}
	return out
}

// SpatialHashIndex 空间哈希网格索引：把动物按坐标分桶到扁平数组（无 map 哈希开销），
// 半径查询只扫描相邻桶，将查询复杂度从 O(动物总数) 降到 O(视野内动物数)。
type SpatialHashIndex struct {
	cellSize  int
	numCellsX int
	cells     [][]*Animal // 长度 numCellsX * numCellsY，用 cellY*numCellsX+cellX 索引
}

func NewSpatialHashIndex(animals []*Animal, cellSize, width, height int) *SpatialHashIndex {
	if cellSize <= 0 {
		cellSize = 8
	}
	numCellsX := (width + cellSize - 1) / cellSize
	numCellsY := (height + cellSize - 1) / cellSize
	idx := &SpatialHashIndex{
		cellSize:  cellSize,
		numCellsX: numCellsX,
		cells:     make([][]*Animal, numCellsX*numCellsY),
	}
	for _, a := range animals {
		if a.Dead {
			continue
		}
		idx.add(a)
	}
	return idx
}

func (s *SpatialHashIndex) cellIndex(x, y int) int {
	return (y/s.cellSize)*s.numCellsX + (x / s.cellSize)
}

func (s *SpatialHashIndex) add(a *Animal) {
	i := s.cellIndex(a.X, a.Y)
	s.cells[i] = append(s.cells[i], a)
}

func (s *SpatialHashIndex) AnimalsInRadius(x, y, r int, species string) []*Animal {
	out := make([]*Animal, 0, 8)
	// 扫描覆盖半径 r 的所有 cell（切比雪夫距离）
	rangeCells := (r + s.cellSize - 1) / s.cellSize
	cx, cy := x/s.cellSize, y/s.cellSize
	for gx := cx - rangeCells; gx <= cx+rangeCells; gx++ {
		if gx < 0 || gx >= s.numCellsX {
			continue
		}
		for gy := cy - rangeCells; gy <= cy+rangeCells; gy++ {
			if gy < 0 {
				continue
			}
			ci := gy*s.numCellsX + gx
			if ci >= len(s.cells) {
				continue
			}
			for _, a := range s.cells[ci] {
				if a.Dead || (species != "" && a.Species != species) {
					continue
				}
				if chebyshev(x, y, a.X, a.Y) <= r {
					out = append(out, a)
				}
			}
		}
	}
	return out
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

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
	River    []bool // 河流格（true=河），河流与草互斥，不长草
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
			River:    make([]bool, cfg.Balance.World.Width*cfg.Balance.World.Height),
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
	// 生成河流：随机选 S 型（蜿蜒直线）或 Y 型（分叉）
	genRiver(w, r)
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

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// genRiver 生成一条蜿蜒穿过地图的河流，随机选择 S 型（蜿蜒）或 Y 型（分叉）。
// 用"随机游走 + 移动平均平滑"生成中心线，蜿蜒自然、无周期性痕迹、连续不断裂。
// 河流格与草互斥：草地设为 0，后续 growGrass 也跳过河流格。
func genRiver(w *World, r *rng.Rng) {
	W, H := w.Grid.W, w.Grid.H

	// 标记一格为河流（含越界保护）
	mark := func(x, y int) {
		if x < 0 || x >= W || y < 0 || y >= H {
			return
		}
		i := w.Grid.Idx(x, y)
		if w.Grid.River[i] {
			return
		}
		w.Grid.River[i] = true
		w.Grid.Grass[i] = 0
		w.Grid.Nutrient[i] = 0
	}

	// 沿中心线序列填充河道：相邻中心点用 Bresenham 线段连接，每段填充方形河宽，
	// 保证任意方向（水平/垂直/斜向）河道都连续不断裂。
	// baseW 为起点宽度，endW 为末端宽度；当 endW < baseW 时河道沿路径逐渐变细（枝干）。
	fillPath := func(path [][2]float64, baseW, endW int, varyW int) {
		if len(path) == 0 {
			return
		}
		n := len(path)
		for i := 0; i+1 < n; i++ {
			x0, y0 := int(path[i][0]), int(path[i][1])
			x1, y1 := int(path[i+1][0]), int(path[i+1][1])
			// Bresenham 线段
			dx := absInt(x1 - x0)
			dy := -absInt(y1 - y0)
			sx := 1
			if x1 < x0 {
				sx = -1
			}
			sy := 1
			if y1 < y0 {
				sy = -1
			}
			// 当前段宽度：按路径进度在 baseW~endW 间线性插值
			segW := float64(baseW) + (float64(endW)-float64(baseW))*float64(i)/float64(n-1)
			err := dx + dy
			for {
				wdt := int(segW + 0.5)
				if varyW > 0 {
					wdt += r.Intn(varyW*2+1) - varyW
				}
				if wdt < 1 {
					wdt = 1
				}
				half := wdt / 2
				for oy := -half; oy <= half; oy++ {
					for ox := -half; ox <= half; ox++ {
						mark(x0+ox, y0+oy)
					}
				}
				if x0 == x1 && y0 == y1 {
					break
				}
				e2 := 2 * err
				if e2 >= dy {
					err += dy
					x0 += sx
				}
				if e2 <= dx {
					err += dx
					y0 += sy
				}
			}
		}
	}

	// 随机游走生成中心线：从起点朝终点逐步移动，带随机垂直扰动
	randomWalk := func(x0, y0, x1, y1 int, steps int, perturb float64) [][2]float64 {
		path := make([][2]float64, 0, steps)
		for i := 0; i < steps; i++ {
			f := float64(i) / float64(steps-1)
			bx := float64(x0) + (float64(x1)-float64(x0))*f // 直线基准位置
			by := float64(y0) + (float64(y1)-float64(y0))*f
			// 垂直扰动（随机游走累积 + 每步噪声）
			px := bx + (r.Float64()*2-1)*perturb
			py := by + (r.Float64()*2-1)*perturb
			path = append(path, [2]float64{px, py})
		}
		return path
	}

	// 移动平均平滑：消除随机噪声的高频抖动，保留自然蜿蜒
	smooth := func(path [][2]float64, radius int) [][2]float64 {
		out := make([][2]float64, len(path))
		for i := range path {
			var sx, sy float64
			cnt := 0
			for j := i - radius; j <= i+radius; j++ {
				if j >= 0 && j < len(path) {
					sx += path[j][0]
					sy += path[j][1]
					cnt++
				}
			}
			out[i] = [2]float64{sx / float64(cnt), sy / float64(cnt)}
		}
		return out
	}

	// mainPaths 收集所有"主干"中心线（S 型 1 条；Y 型主流+两条支流共 3 条）。
	// 之后统一从主干上随机位置分叉出 2~6 条枝干，表现类似血管。
	mainPaths := [][][2]float64{}

	// 随机选形态：S 型 or Y 型
	if r.Intn(2) == 0 {
		// S 型：对角穿过的蜿蜒河，整体呈明显斜率（左上↔右下 或 左下↔右上）
		var path [][2]float64
		steps := int(math.Hypot(float64(W), float64(H))) // 对角线长度
		if r.Intn(2) == 0 {
			// 主对角线方向：左上 → 右下，起止在对角上随机滑动
			x0 := r.Intn(W / 3)
			y0 := r.Intn(H / 3)
			x1 := W - 1 - r.Intn(W/3)
			y1 := H - 1 - r.Intn(H/3)
			path = randomWalk(x0, y0, x1, y1, steps, float64(H)*0.10)
		} else {
			// 副对角线方向：左下 → 右上
			x0 := r.Intn(W / 3)
			y0 := H - 1 - r.Intn(H/3)
			x1 := W - 1 - r.Intn(W/3)
			y1 := r.Intn(H / 3)
			path = randomWalk(x0, y0, x1, y1, steps, float64(H)*0.10)
		}
		path = smooth(path, 3)
		fillPath(path, 4, 4, 1)
		mainPaths = append(mainPaths, path)
	} else {
		// Y 型：主流沿明显斜率从上边某处斜向蜿蜒到中部，再分叉成两条支流
		forkY := int(float64(H) * (0.5 + r.Float64()*0.15))
		forkX := W/2 + r.Intn(W/4) - W/8 // 分叉点 x 略偏离中线，让主流带斜率
		// 主流起点：上边界随机，朝分叉点斜向蜿蜒（带横向跨度，形成斜率）
		startX := r.Intn(W)
		mainPath := randomWalk(startX, 0, forkX, forkY, forkY+1, float64(W)*0.10)
		mainPath = smooth(mainPath, 3)
		fillPath(mainPath, 5, 5, 1)
		mainPaths = append(mainPaths, mainPath)

		// 两条支流：从分叉点向左下、右下
		leftX := r.Intn(W / 3)
		rightX := W - 1 - r.Intn(W/3)
		leftPath := randomWalk(forkX, forkY, leftX, H-1, H-forkY, float64(W)*0.12)
		rightPath := randomWalk(forkX, forkY, rightX, H-1, H-forkY, float64(W)*0.12)
		leftPath = smooth(leftPath, 3)
		rightPath = smooth(rightPath, 3)
		fillPath(leftPath, 3, 3, 1)
		fillPath(rightPath, 3, 3, 1)
		mainPaths = append(mainPaths, leftPath, rightPath)
	}

	// 生成枝干：从主干上随机取分叉点，向外延伸出更细、更短的支流（血管状）。
	// 每条枝干沿随机方向游走，长度约为主干长度的 1/4~1/2，宽度 2~3。
	branches := 2 + r.Intn(5) // 2~6 条
	for b := 0; b < branches; b++ {
		// 随机选一条主干，并在其上取一个分叉点（避开端点）
		mp := mainPaths[r.Intn(len(mainPaths))]
		if len(mp) < 4 {
			continue
		}
		idx := 1 + r.Intn(len(mp)-2)
		sx, sy := mp[idx][0], mp[idx][1]

		// 主干在该点的切线方向（用前后点差分），枝干沿切线向两侧之一偏折
		tx := mp[idx+1][0] - mp[idx-1][0]
		ty := mp[idx+1][1] - mp[idx-1][1]
		// 枝干方向 = 切线旋转 ±(60°~120°)，形成明显分叉角度
		ang := math.Atan2(ty, tx)
		sign := 1.0
		if r.Intn(2) == 0 {
			sign = -1.0
		}
		ang += sign * (math.Pi/3 + r.Float64()*math.Pi/3)
		dirLen := float64(min(W, H)) * (0.25 + r.Float64()*0.25) // 枝干长度 1/4~1/2
		steps := int(dirLen)
		if steps < 8 {
			steps = 8
		}
		bx, by := sx, sy
		branch := make([][2]float64, 0, steps)
		for i := 0; i < steps; i++ {
			bx += math.Cos(ang) + (r.Float64()*2 - 1) * 0.8
			by += math.Sin(ang) + (r.Float64()*2 - 1) * 0.8
			branch = append(branch, [2]float64{bx, by})
		}
		branch = smooth(branch, 2)
		fillPath(branch, 3, 1, 0) // 枝干：起点宽 3，末端收细到 1
	}
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

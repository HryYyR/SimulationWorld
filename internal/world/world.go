package world

import (
	"math"

	"ecosim/internal/config"
	"ecosim/internal/env"
	"ecosim/internal/rng"
)

// 地形类型：用 byte 而非 bool，便于未来扩展湖泊、山地等。
// 每个物种通过 Species.BlockedTerrains 声明自己不能穿越的地形。
const (
	TerrainLand  byte = 0 // 陆地（默认）
	TerrainRiver byte = 1 // 河流
)

// MudWaterThreshold 淤泥判定阈值：河流格水量低于此值视为淤泥（河床裸露），
// 鹿可进入（本质仍是河流格，鳄鱼等水生逻辑不变）。
const MudWaterThreshold = 15.0

type Grid struct {
	W, H     int
	Grass    []float64
	Nutrient []float64
	Terrain  []byte    // 每格地形类型，见 Terrain* 常量
	Water    []float64 // 每格水量（0~100），仅河流格有效
}

func (g *Grid) Idx(x, y int) int { return y*g.W + x }

func (g *Grid) InBounds(x, y int) bool {
	return x >= 0 && y >= 0 && x < g.W && y < g.H
}

// IsRiver 判断某格是否为河流。
func (g *Grid) IsRiver(x, y int) bool {
	return g.InBounds(x, y) && g.Terrain[g.Idx(x, y)] == TerrainRiver
}

// IsMud 判断某河流格是否为淤泥（水量低于阈值，河床裸露）。
func (g *Grid) IsMud(x, y int) bool {
	if !g.IsRiver(x, y) {
		return false
	}
	return g.Water[g.Idx(x, y)] < MudWaterThreshold
}

// IsRiverEdge 判断某河流格是否为河岸边缘格（8 邻域含陆地，越靠近草地）。
func (g *Grid) IsRiverEdge(x, y int) bool {
	if !g.IsRiver(x, y) {
		return false
	}
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dy == 0 {
				continue
			}
			nx, ny := x+dx, y+dy
			if g.InBounds(nx, ny) && g.Terrain[g.Idx(nx, ny)] == TerrainLand {
				return true
			}
		}
	}
	return false
}

// NearRiver 判断某格切比雪夫距离 r 内是否存在河流格（用于岸边植被加速）。
func (g *Grid) NearRiver(x, y, r int) bool {
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if g.IsRiver(x+dx, y+dy) {
				return true
			}
		}
	}
	return false
}

// terrainName 把地形枚举转成配置里使用的名字，与 Species 的 terrains 配置对应。
func terrainName(t byte) string {
	switch t {
	case TerrainRiver:
		return "river"
	default:
		return "land"
	}
}

// IsBlocked 判断某格是否对给定物种不可进入。
// blocked 为黑名单（Species.BlockedTerrains 解析结果），allowed 为白名单
// （Species.AllowedTerrains 解析结果）。白名单非空时优先：必须命中白名单才可通过；
// 否则按黑名单判断。两者都为空则不限制。
// 特殊规则：某物种黑名单含 river 时，若该河流格水量低于淤泥阈值（河床裸露），
// 视为可进入（如鹿可走到干涸的河岸淤泥上）。
func (g *Grid) IsBlocked(x, y int, blocked, allowed map[string]bool) bool {
	if !g.InBounds(x, y) {
		return true
	}
	i := g.Idx(x, y)
	name := terrainName(g.Terrain[i])
	if len(allowed) > 0 {
		return !allowed[name] // 白名单：只能进入声明的地形（如鳄鱼只能在水里）
	}
	if len(blocked) > 0 && blocked[name] {
		// 若被河流阻挡，但该格已是淤泥（水量低于阈值），则允许进入
		if name == "river" && g.Water[i] < MudWaterThreshold {
			return false
		}
		return true
	}
	return false
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
	EggCool  int // 产蛋冷却剩余 tick：产蛋后需间隔，避免每 tick 连续产蛋
	Moved    bool // 本 tick 是否发生过移动（供"静止代谢更低"判定，每 tick 由 Behave 清零）
	PrevX    int  // 上一 tick 所在格（空闲随机移动时尽量不走回，避免原地打转）
	PrevY    int
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

// Egg 鳄鱼蛋等卵生实体的蛋：倒计时孵化，到 0 时产出若干幼崽。
// Energy 记录母体产蛋时转移入蛋的能量，孵化时分配给幼崽（保证能量守恒）。
type Egg struct {
	ID         int
	Species    string
	X, Y       int
	Energy     float64
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
	Eggs         []*Egg
	byID         map[int]*Animal
	NextID       int
	Climate      env.Climate
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
			Terrain:  make([]byte, cfg.Balance.World.Width*cfg.Balance.World.Height),
			Water:    make([]float64, cfg.Balance.World.Width*cfg.Balance.World.Height),
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
		blocked, allowed := sp.BlockedSet(), sp.AllowedSet()
		// 在目标位置附近找一个空位（避开不可进入地形，如鹿避开河流）
		for dx := -2; dx <= 2; dx++ {
			for dy := -2; dy <= 2; dy++ {
				nx, ny := x+dx, y+dy
				if w.Grid.IsBlocked(nx, ny, blocked, allowed) {
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
	// 鳄鱼：只能在水里，因此沿河流格均匀铺开（而非按网格族群分布）
	spawnAquatic(w, cfg, r, occupied)
	// 气候：随机总 tick 并切分四段，初始化温度/降雨量（用 StreamWeather 保证确定性）
	w.Climate = env.InitialClimate(cfg, rng.New(seed, rng.StreamWeather, 0, 0))
	return w
}

// spawnAquatic 为"只能在水中"的物种（allowed_terrains 含 river）沿河流格均匀铺开生成个体。
// 收集全部河流格 → Fisher-Yates 洗牌 → 顺序取前 N 个，保证沿整条河随机散布而非挤在一段。
func spawnAquatic(w *World, cfg *config.Root, r *rng.Rng, occupied map[[2]int]bool) {
	count := cfg.Balance.Init.CrocodileCount
	if count <= 0 {
		return
	}
	sp, ok := cfg.Species["crocodile"]
	if !ok {
		return
	}
	// 收集所有未被占用的河流格
	cells := make([][2]int, 0)
	for y := 0; y < w.Grid.H; y++ {
		for x := 0; x < w.Grid.W; x++ {
			if !w.Grid.IsRiver(x, y) || occupied[[2]int{x, y}] {
				continue
			}
			cells = append(cells, [2]int{x, y})
		}
	}
	if len(cells) == 0 {
		return // 地图无河流，无法生成水生动物
	}
	// Fisher-Yates 洗牌后取前 count 个，实现沿河随机均匀散布
	for i := len(cells) - 1; i > 0; i-- {
		j := r.Intn(i + 1)
		cells[i], cells[j] = cells[j], cells[i]
	}
	n := count
	if n > len(cells) {
		n = len(cells)
	}
	for i := 0; i < n; i++ {
		x, y := cells[i][0], cells[i][1]
		occupied[[2]int{x, y}] = true
		a := w.AddAnimal("crocodile", &sp, x, y, cfg.Balance.EnergyCap,
			rng.New(w.Seed, rng.StreamLifespan, 0, w.NextID).IntRange(sp.Lifespan[0], sp.Lifespan[1]))
		a.Age = sp.Reproduce.MatureAge // 初始为成年
	}
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
		if w.Grid.Terrain[i] == TerrainRiver {
			return
		}
		w.Grid.Terrain[i] = TerrainRiver
		w.Grid.Grass[i] = 0
		w.Grid.Nutrient[i] = 0
		w.Grid.Water[i] = 100 // 初始满水量，河岸格稍后按边缘距离下调
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

	// 河岸边缘格（8 邻域含陆地）初始水量较低，中心格保持满水。
	// 这样旱季河岸先干涸成淤泥，越靠近草地的河岸越容易裸露。
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			if !w.Grid.IsRiver(x, y) {
				continue
			}
			if w.Grid.IsRiverEdge(x, y) {
				w.Grid.Water[w.Grid.Idx(x, y)] = 40 + r.Float64()*20 // 河岸 40~60
			}
		}
	}
}

// CanEnter 统一判断某物种能否进入某格：越界或地形不允许（黑名单/白名单）则不可进入。
func (w *World) CanEnter(x, y int, sp *config.Species) bool {
	return !w.Grid.IsBlocked(x, y, sp.BlockedSet(), sp.AllowedSet())
}

func (w *World) AddAnimal(species string, sp *config.Species, x, y int, energy float64, lifespan int) *Animal {
	a := &Animal{ID: w.NextID, Species: species, X: x, Y: y, Energy: energy, Lifespan: lifespan, Sp: sp}
	a.PrevX, a.PrevY = x, y // 初始上一位置=出生位置（无历史路径）
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

func (w *World) AddEgg(species string, x, y, ticks int, energy float64) *Egg {
	e := &Egg{ID: w.NextID, Species: species, X: x, Y: y, Energy: energy, TotalTicks: ticks, TicksLeft: ticks}
	w.NextID++
	w.Eggs = append(w.Eggs, e)
	return e
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

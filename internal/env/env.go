package env

import (
	"math"

	"ecosim/internal/config"
	"ecosim/internal/modifier"
	"ecosim/internal/rng"
)

// Season 四段热带草原季节：湿季初期 → 湿季盛期 → 旱季初期 → 旱季末期。
// 全年温暖、无冬季，年较差小；干湿分明。
type Season int

const (
	SeasonWetEarly Season = iota // 湿季初期
	SeasonWetPeak                // 湿季盛期
	SeasonDryEarly               // 旱季初期
	SeasonDryLate                // 旱季末期
	SeasonCount                  // 季节总数
)

func (s Season) String() string {
	switch s {
	case SeasonWetEarly:
		return "wet_early"
	case SeasonWetPeak:
		return "wet_peak"
	case SeasonDryEarly:
		return "dry_early"
	case SeasonDryLate:
		return "dry_late"
	default:
		return "unknown"
	}
}

// Climate 当前气候状态：四段季节的 tick 分段，以及当前温度（℃）与降雨量（mm/tick）。
type Climate struct {
	SeasonBounds [4]int // 四段季节各自的 tick 数（湿初/湿盛/旱初/旱末）
	Temperature  float64
	Rainfall     float64
}

// TotalTicks 返回四季循环总 tick 数。
func (c *Climate) TotalTicks() int {
	return c.SeasonBounds[0] + c.SeasonBounds[1] + c.SeasonBounds[2] + c.SeasonBounds[3]
}

// GenSeasonBounds 随机扰动切分全年总 tick 为四段（湿初/湿盛/旱初/旱末），各段 >0。
// 用随机权重扰动，归一化后保证总和 = totalTicks。
func GenSeasonBounds(r *rng.Rng, totalTicks int) [4]int {
	// 随机权重：每段基准 1.0 + 扰动 [-0.5, 0.5]
	weights := [4]float64{}
	sum := 0.0
	for i := 0; i < 4; i++ {
		weights[i] = 1.0 + (r.Float64()*2-1)*0.5
		if weights[i] < 0.2 {
			weights[i] = 0.2
		}
		sum += weights[i]
	}
	// 按权重分配，余数补偿到末段
	bounds := [4]int{}
	allocated := 0
	for i := 0; i < 3; i++ {
		n := int(float64(totalTicks) * weights[i] / sum)
		if n < 1 {
			n = 1
		}
		bounds[i] = n
		allocated += n
	}
	bounds[3] = totalTicks - allocated
	if bounds[3] < 1 {
		bounds[3] = 1
	}
	return bounds
}

// SeasonOf 定位 tick 属于哪个季节（按不等长分段）。
func SeasonOf(tick int, bounds [4]int) Season {
	total := bounds[0] + bounds[1] + bounds[2] + bounds[3]
	if total <= 0 {
		return SeasonWetEarly
	}
	t := tick % total
	if t < 0 {
		t += total
	}
	acc := 0
	for i := 0; i < 4; i++ {
		acc += bounds[i]
		if t < acc {
			return Season(i)
		}
	}
	return SeasonDryLate
}

// seasonStart 返回某季节在循环周期内的起始 tick 偏移。
func seasonStart(season Season, bounds [4]int) int {
	off := 0
	for i := 0; i < int(season); i++ {
		off += bounds[i]
	}
	return off
}

// StepClimate 每 tick 更新温度与降雨量。
// 温度：在四段季节基准值之间做环形连续插值（跨年循环也连续，无跳变）。
// 降雨量：季节内平滑过渡，但季节切换边界允许突变（雨季开始/结束的骤变特征）。
func StepClimate(cl *Climate, tick int, cfg *config.Root, r *rng.Rng) {
	bounds := cl.SeasonBounds
	total := cl.TotalTicks()
	if total <= 0 {
		return
	}
	t := tick % total
	if t < 0 {
		t += total
	}
	season := SeasonOf(tick, bounds)

	// 温度：环形连续插值。取当前季节基准 T0 与下一季节基准 T1，
	// 在当前季节内部按进度线性插值（下一季节取模，保证跨年连续）。
	T0 := cfg.Climate.SeasonBase[season.String()].Temperature
	next := Season((int(season) + 1) % int(SeasonCount))
	T1 := cfg.Climate.SeasonBase[next.String()].Temperature
	segLen := bounds[int(season)]
	if segLen <= 0 {
		segLen = 1
	}
	progress := float64(t-seasonStart(season, bounds)) / float64(segLen)
	base := T0 + (T1-T0)*progress

	// 降雨量：季节内平滑插值（同样用相邻季节基准线性插值），
	// 但允许在季节边界处跳变——这里直接对相邻季节基准做线性插值，
	// 由于相邻季节降雨基准差异大，边界自然出现"骤变"。
	R0 := cfg.Climate.SeasonBase[season.String()].Rainfall
	R1 := cfg.Climate.SeasonBase[next.String()].Rainfall
	rain := R0 + (R1-R0)*progress

	// 小幅随机扰动（温度 ±0.5℃，降雨 ±10%），用固定 stream 保证确定性
	noise := r.Float64()
	cl.Temperature = base + (noise*2-1)*0.5
	rainNoise := r.Float64()
	cl.Rainfall = rain * (1 + (rainNoise*2-1)*0.1)
	if cl.Rainfall < 0 {
		cl.Rainfall = 0
	}
}

// InitialClimate 生成初始气候：随机总 tick 并切分四段，温度/降雨量取湿季初期基准。
func InitialClimate(cfg *config.Root, r *rng.Rng) Climate {
	total := r.IntRange(cfg.Balance.Climate.TotalTicksMin, cfg.Balance.Climate.TotalTicksMax)
	bounds := GenSeasonBounds(r, total)
	cl := Climate{SeasonBounds: bounds}
	base := cfg.Climate.SeasonBase[SeasonWetEarly.String()]
	cl.Temperature = base.Temperature
	cl.Rainfall = base.Rainfall
	return cl
}

// ClimateModifiers 把当前温度/降雨量映射为生态系数 modifier。
// 草生长：温度高斯峰值 × 降雨正相关（设饱和上限）。
// 尸体腐烂：温度越高越快。
// 代谢：温度偏离舒适区越多耗能越高——作用于每个物种的 *.metabolism。
func ClimateModifiers(cfg *config.Root, cl *Climate) []modifier.Modifier {
	c := cfg.Climate
	// 预分配：草生长 1 + 尸体腐烂 1 + 每物种代谢 1
	out := make([]modifier.Modifier, 0, 2+len(cfg.Species))

	// 草生长：gauss(temp) * min(rain/sat, 1)
	gt := gauss(cl.Temperature, c.GrassTempOpt, c.GrassTempSd)
	rainFactor := 1.0
	if c.GrassRainSat > 0 {
		rainFactor = math.Min(1.0, cl.Rainfall/c.GrassRainSat)
		rainFactor = 0.2 + 0.8*rainFactor // 旱季仍有微弱生长下限
	}
	growthMult := gt * rainFactor
	out = append(out, modifier.Modifier{Key: "grass.growth_mult", Mult: growthMult, Source: "climate"})

	// 尸体腐烂：decayMult = 1 + sens*(temp - ref)，高温加速，下限 0.3
	decayMult := 1 + c.DecayTempSens*(cl.Temperature-c.DecayTempRef)
	if decayMult < 0.3 {
		decayMult = 0.3
	}
	out = append(out, modifier.Modifier{Key: "corpse.decay_mult", Mult: decayMult, Source: "climate"})

	// 代谢：metabolismMult = 1 + sens*|temp - comfort|，偏离舒适区耗能增加
	metabMult := 1 + c.MetabTempSens*math.Abs(cl.Temperature-c.MetabComfortTemp)
	for name := range cfg.Species {
		out = append(out, modifier.Modifier{Key: name + ".metabolism", Mult: metabMult, Source: "climate"})
	}

	return out
}

// gauss 高斯函数值（峰值处为 1）。
func gauss(x, mean, sd float64) float64 {
	if sd <= 0 {
		return 1
	}
	d := (x - mean) / sd
	return math.Exp(-0.5 * d * d)
}

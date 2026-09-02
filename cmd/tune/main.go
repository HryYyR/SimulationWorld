package main

import (
	"flag"
	"fmt"
	"os"

	"ecosim/internal/config"
	"ecosim/internal/engine"
)

// 本工具用于在修改参数/逻辑后，快速验证生态平衡情况。
// 用法：
//
//	go run ./cmd/tune                    # 默认 3000 tick，6 个 seed，打印汇总
//	go run ./cmd/tune -ticks 1000        # 指定 tick 数
//	go run ./cmd/tune -seeds 1,2,3       # 指定 seed 列表
//	go run ./cmd/tune -seed 20260901 -curve  # 单 seed + 输出种群曲线
func main() {
	var (
		ticks  = flag.Int("ticks", 3000, "每个 seed 模拟的 tick 数")
		seedStr = flag.String("seeds", "", "seed 列表，逗号分隔（默认 20260901,1,2,3,4,5）")
		single  = flag.Uint64("seed", 0, "只跑单个 seed（0 表示跑 seeds 列表）")
		curve   = flag.Bool("curve", false, "输出种群曲线（每 10% tick 采样一次）")
	)
	flag.Parse()

	seeds := []uint64{20260901, 1, 2, 3, 4, 5}
	if *seedStr != "" {
		seeds = parseSeeds(*seedStr)
	}
	if *single != 0 {
		seeds = []uint64{*single}
	}

	printHeader()

	type stat struct {
		tigerSurvive  int
		deerSurvive   int
		tigerExtinct  string
		deerExtinct   string
		deerMax       int
		deerMin       int
		tigerMax      int
		kills         int
		scavenges     int
	}
	all := stat{deerMin: 1 << 30}
	for _, seed := range seeds {
		s := runSeed(seed, *ticks, *curve)
		printSeed(seed, s, *ticks)
		// 汇总
		if s.tigerExtinctAt == 0 {
			all.tigerSurvive++
		} else {
			all.tigerExtinct += fmt.Sprintf("seed%d@t%d ", seed, s.tigerExtinctAt)
		}
		if s.deerExtinctAt == 0 {
			all.deerSurvive++
		} else {
			all.deerExtinct += fmt.Sprintf("seed%d@t%d ", seed, s.deerExtinctAt)
		}
		if s.deerMax > all.deerMax {
			all.deerMax = s.deerMax
		}
		if s.deerMin < all.deerMin {
			all.deerMin = s.deerMin
		}
		if s.tigerMax > all.tigerMax {
			all.tigerMax = s.tigerMax
		}
		all.kills += s.kills
		all.scavenges += s.scavenges
	}
	printSummary(all, len(seeds))
}

// ---------- 结构体 ----------

type seedStat struct {
	tigerExtinctAt int
	deerExtinctAt  int
	deerMax        int
	deerMin        int
	tigerMax       int
	tigerMin       int
	kills          int
	scavenges      int
}

// ---------- 运行逻辑 ----------

func runSeed(seed uint64, ticks int, wantCurve bool) seedStat {
	cfg, err := config.LoadDir("cfg")
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}
	e := engine.New(cfg, engine.Options{Seed: seed})

	s := seedStat{deerMin: 1 << 30, tigerMin: 1 << 30}
	sampleEvery := ticks / 10
	if sampleEvery < 1 {
		sampleEvery = 1
	}

	err = e.Run(ticks, func(e *engine.Engine) error {
		w := e.World()
		var d, t int
		for _, a := range w.Animals {
			if a.Species == "deer" {
				d++
			} else if a.Species == "tiger" {
				t++
			}
		}
		if d > s.deerMax {
			s.deerMax = d
		}
		if d < s.deerMin {
			s.deerMin = d
		}
		if t > s.tigerMax {
			s.tigerMax = t
		}
		if t > 0 && t < s.tigerMin {
			s.tigerMin = t
		}
		if t == 0 && s.tigerExtinctAt == 0 && w.Tick > 1 {
			s.tigerExtinctAt = w.Tick
		}
		if d == 0 && s.deerExtinctAt == 0 && w.Tick > 1 {
			s.deerExtinctAt = w.Tick
		}
		// 种群曲线采样
		if wantCurve && w.Tick%sampleEvery == 0 {
			var grass float64
			for _, g := range w.Grid.Grass {
				grass += g
			}
			fmt.Printf("    t%5d 虎=%3d 鹿=%5d 草=%8.0f 尸体=%2d\n", w.Tick, t, d, grass, len(w.Corpses))
		}
		return nil
	})
	if err != nil {
		fmt.Printf("    !! 运行错误（可能守恒断言失败）: %v\n", err)
	}
	// 统计（用 Metrics 的逐 tick 样本，避免事件环形缓冲区覆盖导致漏统计）
	for _, m := range e.Metrics() {
		s.kills += m.Kills
		s.scavenges += m.Scavenges
	}
	return s
}

// ---------- 输出 ----------

func printHeader() {
	cfg, err := config.LoadDir("cfg")
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}
	fmt.Println("==================== 生态平衡验证 ====================")
	fmt.Printf("规则版本: %d\n", cfg.Balance.RulesVersion)
	fmt.Printf("初始: 鹿族群=%d 虎族群=%d (每族群 %d 成年 + %d 幼崽)\n",
		cfg.Balance.Init.DeerPacks, cfg.Balance.Init.TigerPacks,
		cfg.Balance.Init.PackAdults, cfg.Balance.Init.PackJuveniles)
	fmt.Printf("虎: 捕猎成功率=%.2f 代谢=%.1f 繁殖成本=%.0f 哺乳比例=%.2f 成熟=%d\n",
		cfg.Species["tiger"].Hunt.Success, cfg.Species["tiger"].Metabolism,
		cfg.Species["tiger"].Reproduce.Cost, cfg.Species["tiger"].Reproduce.NurseRatio,
		cfg.Species["tiger"].Reproduce.MatureAge)
	fmt.Printf("鹿: 代谢=%.1f 繁殖阈值=%.0f 成熟=%d\n",
		cfg.Species["deer"].Metabolism,
		cfg.Species["deer"].Reproduce.EnergyThreshold,
		cfg.Species["deer"].Reproduce.MatureAge)
	fmt.Println("----------------------------------------------------")
}

func printSeed(seed uint64, s seedStat, ticks int) {
	fmt.Printf("\n[seed=%d] 模拟 %d tick\n", seed, ticks)
	if s.tigerExtinctAt > 0 {
		fmt.Printf("  虎: 灭绝于 t%d\n", s.tigerExtinctAt)
	} else {
		fmt.Printf("  虎: 存活（%d tick 内未灭绝）\n", ticks)
	}
	if s.deerExtinctAt > 0 {
		fmt.Printf("  鹿: 灭绝于 t%d\n", s.deerExtinctAt)
	} else {
		fmt.Printf("  鹿: 存活\n")
	}
	tmin := s.tigerMin
	if s.tigerMin == 1<<30 {
		tmin = -1
	}
	fmt.Printf("  虎峰值=%d 虎谷值=%d | 鹿峰值=%d 鹿谷值=%d\n", s.tigerMax, tmin, s.deerMax, s.deerMin)
	fmt.Printf("  捕猎成功=%d 食腐=%d\n", s.kills, s.scavenges)
}

func printSummary(all struct {
	tigerSurvive int
	deerSurvive  int
	tigerExtinct string
	deerExtinct  string
	deerMax      int
	deerMin      int
	tigerMax     int
	kills        int
	scavenges    int
}, n int) {
	fmt.Println("\n==================== 汇总 ====================")
	fmt.Printf("虎存活 %d/%d，鹿存活 %d/%d\n", all.tigerSurvive, n, all.deerSurvive, n)
	if all.tigerExtinct != "" {
		fmt.Printf("虎灭绝明细: %s\n", all.tigerExtinct)
	}
	if all.deerExtinct != "" {
		fmt.Printf("鹿灭绝明细: %s\n", all.deerExtinct)
	}
	fmt.Printf("鹿峰值=%d 鹿谷值=%d 虎峰值=%d\n", all.deerMax, all.deerMin, all.tigerMax)
	fmt.Printf("总捕猎=%d 总食腐=%d\n", all.kills, all.scavenges)
}

func parseSeeds(s string) []uint64 {
	var out []uint64
	cur := ""
	flush := func() {
		if cur == "" {
			return
		}
		var v uint64
		if _, err := fmt.Sscanf(cur, "%d", &v); err == nil {
			out = append(out, v)
		}
		cur = ""
	}
	for _, c := range s {
		if c == ',' {
			flush()
		} else {
			cur += string(c)
		}
	}
	flush()
	return out
}

package main

import (
	"fmt"
	"os"

	"ecosim/internal/config"
	"ecosim/internal/engine"
)

func cheb(x1, y1, x2, y2 int) int {
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

func main() {
	cfg, err := config.LoadDir("cfg")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	e := engine.New(cfg, engine.Options{Seed: 20260901})
	const ticks = 30

	// 初始虎位置 + 鹿分布
	fmt.Println("初始虎：")
	for _, a := range e.World().Animals {
		if a.Species == "tiger" {
			fmt.Printf("  #%d age=%d energy=%.0f pos=(%d,%d)\n", a.ID, a.Age, a.Energy, a.X, a.Y)
		}
	}

	_ = e.Run(ticks, func(e *engine.Engine) error {
		w := e.World()
		if w.Tick%5 == 0 {
			for _, a := range w.Animals {
				if a.Species == "tiger" {
					// 视野内鹿数
					near := 0
					for _, d := range w.Animals {
						if d.Species == "deer" && cheb(a.X, a.Y, d.X, d.Y) <= 8 {
							near++
						}
					}
					fmt.Printf("t%2d 虎#%d energy=%.0f pos=(%d,%d) 视野鹿=%d 草地=%s\n",
						w.Tick, a.ID, a.Energy, a.X, a.Y, near,
						func() string {
							if w.Grid.Grass[w.Grid.Idx(a.X, a.Y)] >= 50 {
								return "隐蔽"
							}
							return "暴露"
						}())
				}
			}
		}
		return nil
	})
}

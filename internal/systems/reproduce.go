package systems

import (
	"sort"

	"ecosim/internal/core"
	"ecosim/internal/world"
)

type Reproduce struct{}

func (s *Reproduce) Name() string { return "reproduce" }

func (s *Reproduce) Step(w *world.World, c *core.Ctx) {
	animals := append([]*world.Animal(nil), w.Animals...)
	sort.Slice(animals, func(i, j int) bool { return animals[i].ID < animals[j].ID })
	for _, a := range animals {
		if a.Dead {
			continue
		}
		if a.Cooldown > 0 {
			a.Cooldown--
		}
		if a.HuntCool > 0 {
			a.HuntCool--
		}
		if a.EggCool > 0 {
			a.EggCool--
		}
	}
}

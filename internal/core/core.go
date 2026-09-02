package core

import (
	"sort"

	"ecosim/internal/config"
	"ecosim/internal/modifier"
	"ecosim/internal/observe"
	"ecosim/internal/world"
)

type Ctx struct {
	Seed     uint64
	Tick     int
	Cfg      *config.Root
	Params   *modifier.Table
	Scoped   []modifier.Scoped
	Ev       *observe.Emitter
	Ledger   *observe.Ledger
	Metrics  *observe.Metrics
	Commands []Command
	Timed    []modifier.Timed
	Index    world.SpatialIndex
}

func (c *Ctx) ResolveAt(key string, x, y int) float64 {
	return c.Params.At(key, x, y)
}

type System interface {
	Name() string
	Step(w *world.World, c *Ctx)
}

type Scheduler struct {
	systems []System
}

func NewScheduler(systems ...System) *Scheduler {
	return &Scheduler{systems: systems}
}

func (s *Scheduler) Systems() []System { return s.systems }

func (s *Scheduler) Run(w *world.World, c *Ctx) {
	for _, sys := range s.systems {
		sys.Step(w, c)
	}
}

type SpawnPayload struct {
	Species string
	X, Y    int
}

type RemovePayload struct {
	EntityID int
}

type WeatherPayload struct {
	State string
	Ticks int
}

type ParamPayload struct {
	Key  string
	Mult float64
	TTL  int
	X, Y int
	R    int
}

type Command struct {
	ID           int
	Tick         int
	Type         string
	Spawn        *SpawnPayload
	Remove       *RemovePayload
	WeatherForce *WeatherPayload
	ParamMod     *ParamPayload
}

func SortCommands(cmds []Command) {
	sort.SliceStable(cmds, func(i, j int) bool {
		if cmds[i].Tick != cmds[j].Tick {
			return cmds[i].Tick < cmds[j].Tick
		}
		return cmds[i].ID < cmds[j].ID
	})
}

package observe

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
	"sort"

	"ecosim/internal/config"
	"ecosim/internal/env"
	"ecosim/internal/world"
)

type Event struct {
	Tick int     `json:"tick"`
	Type string  `json:"type"`
	A    int     `json:"a"`
	B    int     `json:"b"`
	Val  float64 `json:"val"`
}

type Emitter struct {
	events []Event
	head   int
	size   int
}

func NewEmitter(capacity int) *Emitter {
	if capacity <= 0 {
		capacity = 5000
	}
	return &Emitter{events: make([]Event, capacity)}
}

func (e *Emitter) Emit(tick int, typ string, a, b int, val float64) {
	if len(e.events) == 0 {
		return
	}
	e.events[e.head] = Event{Tick: tick, Type: typ, A: a, B: b, Val: val}
	e.head = (e.head + 1) % len(e.events)
	if e.size < len(e.events) {
		e.size++
	}
}

func (e *Emitter) Events() []Event {
	out := make([]Event, 0, e.size)
	start := (e.head - e.size + len(e.events)) % len(e.events)
	for i := 0; i < e.size; i++ {
		out = append(out, e.events[(start+i)%len(e.events)])
	}
	return out
}

func (e *Emitter) Recent(n int) []Event {
	all := e.Events()
	if n <= 0 || n >= len(all) {
		return all
	}
	return append([]Event(nil), all[len(all)-n:]...)
}

type Ledger struct {
	tick   int
	before float64
	flows  map[string]float64
	total  float64
}

func NewLedger() *Ledger {
	return &Ledger{flows: make(map[string]float64)}
}

func (l *Ledger) BeginTick(tick int, total float64) {
	l.tick = tick
	l.before = total
	l.flows = make(map[string]float64)
	l.total = 0
}

// Add records a flow that changes the system's conserved energy total
// (inflow is positive, outflow is negative). Used for the conservation check.
func (l *Ledger) Add(key string, amount float64) {
	l.flows[key] += amount
	l.total += amount
}

// AddTransfer records an internal transfer between energy domains (e.g. grass
// to deer, deer to tiger). It is kept in flows for sankey/observation purposes
// but does not change the conserved total, so it is excluded from the
// conservation check.
func (l *Ledger) AddTransfer(key string, amount float64) {
	l.flows[key] += amount
}

func (l *Ledger) Tick() int { return l.tick }

func (l *Ledger) Flows() map[string]float64 {
	out := make(map[string]float64, len(l.flows))
	for k, v := range l.flows {
		out[k] = v
	}
	return out
}

func (l *Ledger) EndTick(after float64) error {
	delta := after - l.before
	if math.IsNaN(delta) || math.IsInf(delta, 0) {
		return fmt.Errorf("ledger tick %d has non-finite energy delta", l.tick)
	}
	tolerance := 1e-6 * math.Max(1, math.Max(math.Abs(l.before), math.Abs(after)))
	if diff := math.Abs(delta - l.total); diff > tolerance {
		return fmt.Errorf("ledger tick %d leaked energy: delta=%.12g flows=%.12g diff=%.12g", l.tick, delta, l.total, diff)
	}
	return nil
}

type Sample struct {
	Tick           int     `json:"tick"`
	DeerPop        int     `json:"deer_pop"`
	TigerPop       int     `json:"tiger_pop"`
	GrassTotal     float64 `json:"grass_total"`
	NutrientAvg    float64 `json:"nutrient_avg"`
	Births         int     `json:"births"`
	DeathsStarved  int     `json:"deaths_starved"`
	DeathsOld      int     `json:"deaths_old"`
	DeathsPredated int     `json:"deaths_predated"`
	Kills          int     `json:"kills"`
	Scavenges      int     `json:"scavenges"`
}

type Metrics struct {
	samples        []Sample
	head, size     int
	births         int
	deathsStarved  int
	deathsOld      int
	deathsPredated int
	kills          int
	scavenges      int
}

func NewMetrics(capacity int) *Metrics {
	if capacity <= 0 {
		capacity = 20000
	}
	return &Metrics{samples: make([]Sample, capacity)}
}

func (m *Metrics) Birth()       { m.births++ }
func (m *Metrics) Starved()     { m.deathsStarved++ }
func (m *Metrics) OldAge()      { m.deathsOld++ }
func (m *Metrics) Predated()    { m.deathsPredated++; m.kills++ }
func (m *Metrics) HuntSuccess() { m.kills++ }
func (m *Metrics) Scavenge()    { m.scavenges++ }

func (m *Metrics) Take(w *world.World) Sample {
	s := Sample{
		Tick:           w.Tick,
		Births:         m.births,
		DeathsStarved:  m.deathsStarved,
		DeathsOld:      m.deathsOld,
		DeathsPredated: m.deathsPredated,
		Kills:          m.kills,
		Scavenges:      m.scavenges,
	}
	for _, a := range w.Animals {
		switch a.Species {
		case "deer":
			s.DeerPop++
		case "tiger":
			s.TigerPop++
		}
	}
	for _, g := range w.Grid.Grass {
		s.GrassTotal += g
	}
	for _, n := range w.Grid.Nutrient {
		s.NutrientAvg += n
	}
	if len(w.Grid.Nutrient) > 0 {
		s.NutrientAvg /= float64(len(w.Grid.Nutrient))
	}
	m.samples[m.head] = s
	m.head = (m.head + 1) % len(m.samples)
	if m.size < len(m.samples) {
		m.size++
	}
	m.births = 0
	m.deathsStarved = 0
	m.deathsOld = 0
	m.deathsPredated = 0
	m.kills = 0
	m.scavenges = 0
	return s
}

func (m *Metrics) Samples() []Sample {
	out := make([]Sample, 0, m.size)
	start := (m.head - m.size + len(m.samples)) % len(m.samples)
	for i := 0; i < m.size; i++ {
		out = append(out, m.samples[(start+i)%len(m.samples)])
	}
	return out
}

func (m *Metrics) Recent(n int) []Sample {
	all := m.Samples()
	if n <= 0 || n >= len(all) {
		return all
	}
	return append([]Sample(nil), all[len(all)-n:]...)
}

type AnimalView struct {
	ID        int     `json:"id"`
	Species   string  `json:"species"`
	X         int     `json:"x"`
	Y         int     `json:"y"`
	Age       int     `json:"age"`
	MatureAge int     `json:"mature_age"`
	Energy    float64 `json:"energy"`
	Lifespan  int     `json:"lifespan"`
	Cooldown  int     `json:"cooldown"`
}

type CorpseView struct {
	ID        int     `json:"id"`
	Species   string  `json:"species"`
	X         int     `json:"x"`
	Y         int     `json:"y"`
	Remaining float64 `json:"remaining"`
	Total     float64 `json:"total"`
	TicksLeft int     `json:"ticks_left"`
}

type Snapshot struct {
	RulesVersion int              `json:"rules_version"`
	Tick         int              `json:"tick"`
	Seed         uint64           `json:"seed"`
	Width        int              `json:"width"`
	Height       int              `json:"height"`
	Grass        []float64        `json:"grass"`
	Nutrient     []float64        `json:"nutrient"`
	Terrain      []int            `json:"terrain,omitempty"`
	Animals      []AnimalView     `json:"animals"`
	Corpses      []CorpseView     `json:"corpses"`
	Weather      env.WeatherState `json:"weather"`
	StateHash    uint64           `json:"state_hash"`
	Events       []Event          `json:"events"`
	Samples      []Sample         `json:"samples"`
}

// terrainToInts 把字节地形转成 int 切片，确保 JSON 序列化为 [0,1,...] 数组而非 base64 字符串。
func terrainToInts(t []byte) []int {
	out := make([]int, len(t))
	for i, v := range t {
		out[i] = int(v)
	}
	return out
}

func SnapshotFromWorld(w *world.World, cfg *config.Root, events []Event, samples []Sample) *Snapshot {
	s := &Snapshot{
		RulesVersion: w.RulesVersion,
		Tick:         w.Tick,
		Seed:         w.Seed,
		Width:        w.Grid.W,
		Height:       w.Grid.H,
		Grass:        append([]float64(nil), w.Grid.Grass...),
		Nutrient:     append([]float64(nil), w.Grid.Nutrient...),
		Terrain:      terrainToInts(w.Grid.Terrain),
		Weather:      w.Weather,
		StateHash:    StateHash(w),
		Events:       append([]Event(nil), events...),
		Samples:      append([]Sample(nil), samples...),
	}
	// 初始化为空切片（而非 nil），保证 JSON 序列化为 [] 而非 null，避免前端判空错误
	s.Animals = []AnimalView{}
	s.Corpses = []CorpseView{}
	s.Events = append([]Event{}, events...)
	s.Samples = append([]Sample{}, samples...)

	for _, a := range w.Animals {
		s.Animals = append(s.Animals, AnimalView{
			ID: a.ID, Species: a.Species, X: a.X, Y: a.Y,
			Age: a.Age, MatureAge: cfg.Species[a.Species].Reproduce.MatureAge,
			Energy: a.Energy, Lifespan: a.Lifespan, Cooldown: a.Cooldown,
		})
	}
	for _, c := range w.Corpses {
		s.Corpses = append(s.Corpses, CorpseView{
			ID: c.ID, Species: c.Species, X: c.X, Y: c.Y,
			Remaining: c.Remaining, Total: c.Total, TicksLeft: c.TicksLeft,
		})
	}
	return s
}

func StateHash(w *world.World) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	put := func(v uint64) {
		binary.LittleEndian.PutUint64(buf[:], v)
		_, _ = h.Write(buf[:])
	}
	putFloat := func(v float64) { put(math.Float64bits(v)) }
	putString := func(s string) {
		put(uint64(len(s)))
		_, _ = h.Write([]byte(s))
	}
	put(uint64(w.RulesVersion))
	put(w.Seed)
	put(uint64(w.Tick))
	put(uint64(w.Grid.W))
	put(uint64(w.Grid.H))
	for _, v := range w.Grid.Grass {
		putFloat(v)
	}
	for _, v := range w.Grid.Nutrient {
		putFloat(v)
	}
	for _, v := range w.Grid.Terrain {
		put(uint64(v))
	}
	animals := append([]*world.Animal(nil), w.Animals...)
	sort.Slice(animals, func(i, j int) bool { return animals[i].ID < animals[j].ID })
	put(uint64(len(animals)))
	for _, a := range animals {
		put(uint64(a.ID))
		putString(a.Species)
		put(uint64(a.X))
		put(uint64(a.Y))
		put(uint64(a.Age))
		putFloat(a.Energy)
		put(uint64(a.Lifespan))
		put(uint64(a.Cooldown))
		if a.Dead {
			put(1)
		} else {
			put(0)
		}
	}
	corpses := append([]*world.Corpse(nil), w.Corpses...)
	sort.Slice(corpses, func(i, j int) bool { return corpses[i].ID < corpses[j].ID })
	put(uint64(len(corpses)))
	for _, c := range corpses {
		put(uint64(c.ID))
		putString(c.Species)
		put(uint64(c.X))
		put(uint64(c.Y))
		putFloat(c.Total)
		putFloat(c.Remaining)
		put(uint64(c.TotalTicks))
		put(uint64(c.TicksLeft))
	}
	putString(w.Weather.Current)
	put(uint64(w.Weather.Left))
	return h.Sum64()
}

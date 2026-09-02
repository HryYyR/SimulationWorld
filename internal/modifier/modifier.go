package modifier

import "sort"

type Modifier struct {
	Key      string
	Mult     float64
	Add      float64
	Source   string
	Priority int
}

type Scoped struct {
	Modifier
	X, Y, R int
}

type Timed struct {
	Modifier
	X, Y, R int
	TTL     int
}

type Table struct {
	values map[string]float64
	scoped map[string][]Scoped
}

func Resolve(base map[string]float64, globals []Modifier, scoped []Scoped) *Table {
	g := make([]Modifier, len(globals))
	copy(g, globals)
	sort.Slice(g, func(i, j int) bool {
		if g[i].Key != g[j].Key {
			return g[i].Key < g[j].Key
		}
		if g[i].Priority != g[j].Priority {
			return g[i].Priority < g[j].Priority
		}
		return g[i].Source < g[j].Source
	})

	t := &Table{
		values: make(map[string]float64, len(base)),
		scoped: make(map[string][]Scoped),
	}
	for k, v := range base {
		t.values[k] = v
	}
	for _, m := range g {
		t.values[m.Key] += m.Add
	}
	for _, m := range g {
		if m.Mult != 0 {
			t.values[m.Key] *= m.Mult
		}
	}

	s := make([]Scoped, len(scoped))
	copy(s, scoped)
	sort.Slice(s, func(i, j int) bool {
		if s[i].Key != s[j].Key {
			return s[i].Key < s[j].Key
		}
		if s[i].Priority != s[j].Priority {
			return s[i].Priority < s[j].Priority
		}
		return s[i].Source < s[j].Source
	})
	for _, m := range s {
		t.scoped[m.Key] = append(t.scoped[m.Key], m)
	}
	return t
}

func (t *Table) Get(key string) float64 {
	if t == nil {
		return 0
	}
	return t.values[key]
}

func (t *Table) ScopedFor(key string) []Scoped {
	if t == nil {
		return nil
	}
	return t.scoped[key]
}

func (t *Table) At(key string, x, y int) float64 {
	v := t.Get(key)
	if t == nil {
		return v
	}
	for _, m := range t.scoped[key] {
		if m.R <= 0 || chebyshev(x, y, m.X, m.Y) <= m.R {
			if m.Mult != 0 {
				v *= m.Mult
			}
		}
	}
	return v
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

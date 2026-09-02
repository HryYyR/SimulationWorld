package systems

import (
	"math"

	"ecosim/internal/core"
	"ecosim/internal/world"
)

type LedgerMetrics struct{}

func (s *LedgerMetrics) Name() string { return "ledgerAndMetrics" }

func (s *LedgerMetrics) Step(w *world.World, c *core.Ctx) {
	after := TotalEnergy(w)
	if err := c.Ledger.EndTick(after); err != nil {
		panic(err)
	}
	if err := validateFinite(w); err != nil {
		panic(err)
	}
	c.Metrics.Take(w)
}

func TotalEnergy(w *world.World) float64 {
	total := 0.0
	for _, v := range w.Grid.Grass {
		total += v
	}
	for _, a := range w.Animals {
		total += a.Energy
	}
	return total
}

func validateFinite(w *world.World) error {
	for i, v := range w.Grid.Grass {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return &worldError{Field: "grass", Index: i}
		}
	}
	for i, v := range w.Grid.Nutrient {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return &worldError{Field: "nutrient", Index: i}
		}
	}
	for _, a := range w.Animals {
		if math.IsNaN(a.Energy) || math.IsInf(a.Energy, 0) {
			return &worldError{Field: "animal_energy", Index: a.ID}
		}
	}
	return nil
}

type worldError struct {
	Field string
	Index int
}

func (e *worldError) Error() string {
	return "non-finite value in " + e.Field + " at " + itoa(e.Index)
}

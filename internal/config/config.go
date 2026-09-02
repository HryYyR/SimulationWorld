package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Root struct {
	Balance   Balance            `json:"balance"`
	Species   map[string]Species `json:"species"`
	Weather   Weather            `json:"weather"`
	HashValue string             `json:"config_hash,omitempty"`
}

type Balance struct {
	RulesVersion int `json:"rules_version"`
	World        struct {
		Width  int `json:"width"`
		Height int `json:"height"`
		Seed   int `json:"seed"`
	} `json:"world"`
	Time struct {
		TicksPerSeason int `json:"ticks_per_season"`
	} `json:"time"`
	Grass struct {
		GrowthBase               float64 `json:"growth_base"`
		GrowthNutrientCoeff      float64 `json:"growth_nutrient_coeff"`
		GrowthCap                float64 `json:"growth_cap"`
		NutrientConsumptionCoeff float64 `json:"nutrient_consumption_coeff"`
		NutrientCap              float64 `json:"nutrient_cap"`
	} `json:"grass"`
	Init struct {
		Grass         float64 `json:"grass"`
		Nutrient      float64 `json:"nutrient"`
		Noise         float64 `json:"noise"`
		DeerPacks     int     `json:"deer_packs"`
		TigerPacks    int     `json:"tiger_packs"`
		PackAdults    int     `json:"pack_adults"`
		PackJuveniles int     `json:"pack_juveniles"`
	} `json:"init"`
	EnergyCap float64 `json:"energy_cap"`
	Scavenge  struct {
		Rate       float64 `json:"rate"`
		Efficiency float64 `json:"efficiency"`
	} `json:"scavenge"`
}

type Species struct {
	Vision                 int       `json:"vision"`
	ThreatRadius           int       `json:"threat_radius,omitempty"`
	MoveCost               float64   `json:"move_cost"`
	Metabolism             float64   `json:"metabolism"`
	Diet                   Diet      `json:"diet,omitempty"`
	EatThreshold           float64   `json:"eat_threshold,omitempty"`
	Hunt                   Hunt      `json:"hunt,omitempty"`
	Reproduce              Reproduce `json:"reproduce"`
	Lifespan               [2]int    `json:"lifespan"`
	JuvenileMetabolismMult float64   `json:"juvenile_metabolism_mult"`
	JuvenileEnergyCapMult  float64   `json:"juvenile_energy_cap_mult,omitempty"`
	OldMetabolismMult      float64   `json:"old_metabolism_mult,omitempty"`
	SatiationFull          float64   `json:"satiation_full,omitempty"`
	SatiationHungry        float64   `json:"satiation_hungry,omitempty"`
	ScavengeFreshness      float64   `json:"scavenge_freshness,omitempty"`
	AvoidRadius            int       `json:"avoid_radius,omitempty"`
	KillRadius             int       `json:"kill_radius,omitempty"`
	HuntCooldown           int       `json:"hunt_cooldown,omitempty"`
	Corpse                 Corpse    `json:"corpse"`
}

type Diet struct {
	Rate         float64 `json:"rate"`
	Efficiency   float64 `json:"efficiency"`
	DungNutrient float64 `json:"dung_nutrient"`
}

type Hunt struct {
	Success       float64 `json:"success"`
	Gain          float64 `json:"gain"`
	FailTigerCost float64 `json:"fail_tiger_cost"`
	FailDeerCost  float64 `json:"fail_deer_cost"`
	FleeJump      int     `json:"flee_jump"`
}

type Reproduce struct {
	EnergyThreshold float64 `json:"energy_threshold"`
	Cooldown        int     `json:"cooldown"`
	Cost            float64 `json:"cost"`
	ChildEnergy     float64 `json:"child_energy"`
	MatureAge       int     `json:"mature_age"`
	MatingMode      string  `json:"mating_mode"`
	NurseRatio      float64 `json:"nurse_ratio,omitempty"`
}

type Corpse struct {
	Ticks    int     `json:"ticks"`
	Nutrient float64 `json:"nutrient"`
}

type Weather struct {
	States          []string                        `json:"states"`
	Duration        map[string][2]int               `json:"duration"`
	Transitions     map[string]map[string][]float64 `json:"transitions"`
	Modifiers       map[string][]ModifierSpec       `json:"modifiers"`
	SeasonModifiers map[string][]ModifierSpec       `json:"season_modifiers"`
}

type ModifierSpec struct {
	Key      string  `json:"key"`
	Mult     float64 `json:"mult,omitempty"`
	Add      float64 `json:"add,omitempty"`
	Priority int     `json:"priority,omitempty"`
}

func LoadDir(dir string) (*Root, error) {
	balance, err := readJSON[BalJSON](filepath.Join(dir, "balance.json"))
	if err != nil {
		return nil, fmt.Errorf("load balance: %w", err)
	}
	species, err := readJSON[map[string]Species](filepath.Join(dir, "species.json"))
	if err != nil {
		return nil, fmt.Errorf("load species: %w", err)
	}
	weather, err := readJSON[Weather](filepath.Join(dir, "weather.json"))
	if err != nil {
		return nil, fmt.Errorf("load weather: %w", err)
	}
	r := &Root{Balance: balance, Species: species, Weather: weather}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	r.HashValue = r.hash()
	return r, nil
}

type BalJSON = Balance

func readJSON[T any](path string) (T, error) {
	var v T
	b, err := os.ReadFile(path)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}

func (r *Root) Validate() error {
	if r.Balance.RulesVersion <= 0 {
		return fmt.Errorf("rules_version must be positive")
	}
	if r.Balance.World.Width <= 0 || r.Balance.World.Height <= 0 {
		return fmt.Errorf("world dimensions must be positive")
	}
	if r.Balance.Time.TicksPerSeason <= 0 {
		return fmt.Errorf("ticks_per_season must be positive")
	}
	if _, ok := r.Species["deer"]; !ok {
		return fmt.Errorf("species deer is required")
	}
	if _, ok := r.Species["tiger"]; !ok {
		return fmt.Errorf("species tiger is required")
	}
	for name, sp := range r.Species {
		if sp.Lifespan[0] <= 0 || sp.Lifespan[1] < sp.Lifespan[0] {
			return fmt.Errorf("species %s has invalid lifespan", name)
		}
		if sp.Reproduce.MatureAge <= 0 || sp.Reproduce.Cooldown < 0 {
			return fmt.Errorf("species %s has invalid reproduce values", name)
		}
		if sp.Corpse.Ticks <= 0 || sp.Corpse.Nutrient < 0 {
			return fmt.Errorf("species %s has invalid corpse values", name)
		}
	}
	if len(r.Weather.States) == 0 {
		return fmt.Errorf("weather states are required")
	}
	for state, row := range r.Weather.Transitions {
		for from, probs := range row {
			if len(probs) != len(r.Weather.States) {
				return fmt.Errorf("weather transition %s/%s has wrong length", state, from)
			}
			sum := 0.0
			for _, p := range probs {
				if p < 0 || p > 1 {
					return fmt.Errorf("weather transition %s/%s has invalid probability", state, from)
				}
				sum += p
			}
			if sum <= 0.99 || sum >= 1.01 {
				return fmt.Errorf("weather transition %s/%s probabilities sum to %g", state, from, sum)
			}
		}
	}
	return nil
}

func (r *Root) hash() string {
	type plain Root
	p := plain(*r)
	p.HashValue = ""
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (r *Root) Hash() string { return r.HashValue }

func (r *Root) BaseSlots() map[string]float64 {
	m := map[string]float64{
		"grass.growth_mult":                1,
		"grass.growth_base":                r.Balance.Grass.GrowthBase,
		"grass.growth_nutrient_coeff":      r.Balance.Grass.GrowthNutrientCoeff,
		"grass.growth_cap":                 r.Balance.Grass.GrowthCap,
		"grass.nutrient_consumption_coeff": r.Balance.Grass.NutrientConsumptionCoeff,
		"grass.nutrient_cap":               r.Balance.Grass.NutrientCap,
		"global.energy_cap":                r.Balance.EnergyCap,
		"global.scavenge_rate":             r.Balance.Scavenge.Rate,
		"global.scavenge_efficiency":       r.Balance.Scavenge.Efficiency,
	}
	for name, sp := range r.Species {
		prefix := name + "."
		m[prefix+"vision"] = float64(sp.Vision)
		m[prefix+"threat_radius"] = float64(sp.ThreatRadius)
		m[prefix+"move_cost"] = sp.MoveCost
		m[prefix+"metabolism"] = sp.Metabolism
		m[prefix+"eat_threshold"] = sp.EatThreshold
		m[prefix+"juvenile_metabolism_mult"] = sp.JuvenileMetabolismMult
		m[prefix+"juvenile_energy_cap_mult"] = sp.JuvenileEnergyCapMult
		m[prefix+"old_metabolism_mult"] = sp.OldMetabolismMult
		m[prefix+"satiation_full"] = sp.SatiationFull
		m[prefix+"satiation_hungry"] = sp.SatiationHungry
		m[prefix+"scavenge_freshness"] = sp.ScavengeFreshness
		m[prefix+"avoid_radius"] = float64(sp.AvoidRadius)
		m[prefix+"kill_radius"] = float64(sp.KillRadius)
		m[prefix+"hunt_cooldown"] = float64(sp.HuntCooldown)
		m[prefix+"reproduce_threshold"] = sp.Reproduce.EnergyThreshold
		m[prefix+"reproduce_cooldown"] = float64(sp.Reproduce.Cooldown)
		m[prefix+"reproduce_cost"] = sp.Reproduce.Cost
		m[prefix+"child_energy"] = sp.Reproduce.ChildEnergy
		m[prefix+"mature_age"] = float64(sp.Reproduce.MatureAge)
		m[prefix+"nurse_ratio"] = sp.Reproduce.NurseRatio
		m[prefix+"lifespan_min"] = float64(sp.Lifespan[0])
		m[prefix+"lifespan_max"] = float64(sp.Lifespan[1])
		m[prefix+"corpse_ticks"] = float64(sp.Corpse.Ticks)
		m[prefix+"corpse_nutrient"] = sp.Corpse.Nutrient
		if name == "deer" {
			m[prefix+"eat_rate"] = sp.Diet.Rate
			m[prefix+"eat_efficiency"] = sp.Diet.Efficiency
			m[prefix+"dung_nutrient"] = sp.Diet.DungNutrient
		}
		if name == "tiger" {
			m[prefix+"hunt_success"] = sp.Hunt.Success
			m[prefix+"hunt_gain"] = sp.Hunt.Gain
			m[prefix+"hunt_fail_tiger_cost"] = sp.Hunt.FailTigerCost
			m[prefix+"hunt_fail_deer_cost"] = sp.Hunt.FailDeerCost
			m[prefix+"hunt_flee_jump"] = float64(sp.Hunt.FleeJump)
		}
	}
	return m
}

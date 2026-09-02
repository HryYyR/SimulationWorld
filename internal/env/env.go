package env

import (
	"fmt"

	"ecosim/internal/config"
	"ecosim/internal/modifier"
	"ecosim/internal/rng"
)

type Season int

const (
	Spring Season = iota
	Summer
	Autumn
	Winter
)

func (s Season) String() string {
	switch s {
	case Spring:
		return "spring"
	case Summer:
		return "summer"
	case Autumn:
		return "autumn"
	case Winter:
		return "winter"
	default:
		return "unknown"
	}
}

func SeasonOf(tick, perSeason int) Season {
	if perSeason <= 0 {
		perSeason = 1
	}
	return Season((tick / perSeason) % 4)
}

type WeatherState struct {
	Current string
	Left    int
}

func InitialWeather(cfg *config.Root, r *rng.Rng) WeatherState {
	state := cfg.Weather.States[0]
	d := cfg.Weather.Duration[state]
	return WeatherState{Current: state, Left: r.IntRange(d[0], d[1])}
}

func AdvanceWeather(cfg *config.Root, season Season, cur WeatherState, r *rng.Rng) (WeatherState, bool) {
	if cur.Left > 0 {
		cur.Left--
		return cur, false
	}
	name := season.String()
	row, ok := cfg.Weather.Transitions[name][cur.Current]
	if !ok {
		return cur, false
	}
	pick := r.Float64()
	sum := 0.0
	next := cfg.Weather.States[len(cfg.Weather.States)-1]
	for i, p := range row {
		sum += p
		if pick < sum {
			next = cfg.Weather.States[i]
			break
		}
	}
	d := cfg.Weather.Duration[next]
	return WeatherState{Current: next, Left: r.IntRange(d[0], d[1])}, true
}

func WeatherModifiers(cfg *config.Root, state string) []modifier.Modifier {
	out := make([]modifier.Modifier, 0)
	for _, m := range cfg.Weather.Modifiers[state] {
		out = append(out, modifier.Modifier{
			Key: m.Key, Mult: m.Mult, Add: m.Add,
			Source: "weather." + state, Priority: m.Priority,
		})
	}
	return out
}

func SeasonModifiers(cfg *config.Root, season Season) []modifier.Modifier {
	out := make([]modifier.Modifier, 0)
	for _, m := range cfg.Weather.SeasonModifiers[season.String()] {
		out = append(out, modifier.Modifier{
			Key: m.Key, Mult: m.Mult, Add: m.Add,
			Source: "season." + season.String(), Priority: m.Priority,
		})
	}
	return out
}

func ValidateState(cfg *config.Root, state string) error {
	for _, s := range cfg.Weather.States {
		if s == state {
			return nil
		}
	}
	return fmt.Errorf("unknown weather state %q", state)
}

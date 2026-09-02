package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"ecosim/internal/config"
	"ecosim/internal/engine"
	"ecosim/internal/observe"
)

type survivalReport struct {
	Tick         int     `json:"tick"`
	DeerPop      int     `json:"deer_pop"`
	TigerPop     int     `json:"tiger_pop"`
	GrassTotal   float64 `json:"grass_total"`
	NutrientAvg  float64 `json:"nutrient_avg"`
	StateHash    string  `json:"state_hash"`
	Extinct      bool    `json:"extinct"`
	RulesVersion int     `json:"rules_version"`
}

func main() {
	var (
		seed       uint64
		ticks      int
		cfgDir     string
		outDir     string
		replayPath string
		tapePath   string
	)
	flag.Uint64Var(&seed, "seed", 0, "simulation seed (0 uses balance.json)")
	flag.IntVar(&ticks, "ticks", 10000, "number of ticks")
	flag.StringVar(&cfgDir, "cfg", "cfg", "configuration directory")
	flag.StringVar(&outDir, "out", "result", "output directory")
	flag.StringVar(&replayPath, "replay", "", "replay tape JSON")
	flag.StringVar(&tapePath, "tape", "", "write a replay tape to this path")
	flag.Parse()

	cfg, err := config.LoadDir(cfgDir)
	if err != nil {
		fatal(err)
	}
	if replayPath != "" {
		tape, err := loadTape(replayPath)
		if err != nil {
			fatal(err)
		}
		if err := engine.Replay(cfg, tape); err != nil {
			fatal(err)
		}
		fmt.Printf("replay ok: %d ticks\n", tape.Ticks)
		return
	}
	if seed == 0 {
		seed = uint64(cfg.Balance.World.Seed)
	}
	if ticks < 0 {
		fatal(fmt.Errorf("ticks must be non-negative"))
	}

	e := engine.New(cfg, engine.Options{Seed: seed})
	if err := e.Run(ticks, nil); err != nil {
		fatal(err)
	}
	if err := writeOutputs(e, cfg, outDir, ticks); err != nil {
		fatal(err)
	}
	if tapePath != "" {
		if err := writeTape(e, tapePath, ticks); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("tick=%d state_hash=%016x\n", e.World().Tick, e.StateHash())
}

func loadTape(path string) (engine.Tape, error) {
	var tape engine.Tape
	b, err := os.ReadFile(path)
	if err != nil {
		return tape, err
	}
	if err := json.Unmarshal(b, &tape); err != nil {
		return tape, fmt.Errorf("decode tape %s: %w", path, err)
	}
	return tape, nil
}

func writeOutputs(e *engine.Engine, cfg *config.Root, outDir string, ticks int) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := writePopulation(filepath.Join(outDir, "population.csv"), e.Metrics()); err != nil {
		return err
	}
	if err := writeEvents(filepath.Join(outDir, "events.ndjson"), e.Events()); err != nil {
		return err
	}
	report := buildReport(e, cfg)
	if err := writeJSON(filepath.Join(outDir, "survival.json"), report); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "state_hash.txt"), []byte(report.StateHash+"\n"), 0o644)
}

func writePopulation(path string, samples []observe.Sample) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{
		"tick", "deer_pop", "tiger_pop", "grass_total", "nutrient_avg",
		"births", "deaths_starved", "deaths_old", "deaths_predated", "kills",
	}); err != nil {
		return err
	}
	for _, s := range samples {
		row := []string{
			strconv.Itoa(s.Tick), strconv.Itoa(s.DeerPop), strconv.Itoa(s.TigerPop),
			strconv.FormatFloat(s.GrassTotal, 'f', -1, 64),
			strconv.FormatFloat(s.NutrientAvg, 'f', -1, 64),
			strconv.Itoa(s.Births), strconv.Itoa(s.DeathsStarved), strconv.Itoa(s.DeathsOld),
			strconv.Itoa(s.DeathsPredated), strconv.Itoa(s.Kills),
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return w.Error()
}

func writeEvents(path string, events []observe.Event) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return nil
}

func buildReport(e *engine.Engine, cfg *config.Root) survivalReport {
	var deer, tiger int
	var grass, nutrient float64
	for _, a := range e.World().Animals {
		switch a.Species {
		case "deer":
			deer++
		case "tiger":
			tiger++
		}
	}
	for _, v := range e.World().Grid.Grass {
		grass += v
	}
	for _, v := range e.World().Grid.Nutrient {
		nutrient += v
	}
	if len(e.World().Grid.Nutrient) > 0 {
		nutrient /= float64(len(e.World().Grid.Nutrient))
	}
	return survivalReport{
		Tick: e.World().Tick, DeerPop: deer, TigerPop: tiger,
		GrassTotal: grass, NutrientAvg: nutrient,
		StateHash: fmt.Sprintf("%016x", e.StateHash()),
		Extinct:   deer == 0 || tiger == 0, RulesVersion: cfg.Balance.RulesVersion,
	}
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func writeTape(e *engine.Engine, path string, ticks int) error {
	return writeJSON(path, e.Tape(ticks))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "sim:", err)
	os.Exit(1)
}

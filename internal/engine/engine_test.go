package engine

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ecosim/internal/config"
)

// update regenerates the golden file when -update is passed to `go test`.
var update = flag.Bool("update", false, "update golden files")

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// goldenFile records the deterministic end-state hash for a fixed seed+config.
// It is the acceptance checkpoint for the determinism foundation (§18 M0):
// same seed + config must produce the identical hash on every run/platform.
type goldenFile struct {
	Seed         uint64 `json:"seed"`
	RulesVersion int    `json:"rules_version"`
	Ticks        int    `json:"ticks"`
	StateHash    string `json:"state_hash"`
}

const (
	goldenTicks = 1000
	conservTicks = 2000
)

func loadConfig(t *testing.T) *config.Root {
	t.Helper()
	cfg, err := config.LoadDir(filepath.Join("..", "..", "cfg"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func runWorld(t *testing.T, ticks int) *Engine {
	t.Helper()
	cfg := loadConfig(t)
	e := New(cfg, Options{Seed: uint64(cfg.Balance.World.Seed)})
	if err := e.Run(ticks, nil); err != nil {
		t.Fatalf("run %d ticks: %v", ticks, err)
	}
	return e
}

func formatHash(h uint64) string { return fmt.Sprintf("%016x", h) }

// TestGoldenHash runs a fixed seed+config for 1000 ticks and compares the final
// StateHash against testdata/golden.json. Regenerate with `go test -run TestGoldenHash -update`.
func TestGoldenHash(t *testing.T) {
	e := runWorld(t, goldenTicks)
	got := formatHash(e.StateHash())

	path := filepath.Join("testdata", "golden.json")
	if *update {
		g := goldenFile{
			Seed:         e.world.Seed,
			RulesVersion: e.cfg.Balance.RulesVersion,
			Ticks:        goldenTicks,
			StateHash:    got,
		}
		b, err := json.MarshalIndent(g, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", path)
		return
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create it)", path, err)
	}
	var g goldenFile
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("decode golden %s: %v", path, err)
	}
	if g.StateHash != got {
		t.Errorf("state hash mismatch:\n  got  %s\n  want %s\n"+
			"rules or pipeline order changed without bumping rules_version; "+
			"if intentional, bump rules_version then re-run with -update", got, g.StateHash)
	}
}

// TestLedgerConservation runs 2000 ticks. Any energy leak trips the ledger's
// conservation assertion (a panic) inside LedgerMetrics, failing this test.
func TestLedgerConservation(t *testing.T) {
	runWorld(t, conservTicks)
}

// TestSmoke runs 100 ticks and only requires a clean exit.
func TestSmoke(t *testing.T) {
	runWorld(t, 100)
}

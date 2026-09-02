package engine

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"strconv"

	"ecosim/internal/config"
	"ecosim/internal/core"
	"ecosim/internal/observe"
	"ecosim/internal/systems"
	"ecosim/internal/world"
)

const (
	DefaultSnapshotInterval = 500
	DefaultSnapshotKeep     = 8
)

type Options struct {
	Seed             uint64
	Commands         []core.Command
	SnapshotInterval int
	SnapshotKeep     int
}

type SnapshotRecord struct {
	Tick int
	Hash uint64
	Data []byte
}

type Engine struct {
	cfg       *config.Root
	world     *world.World
	ctx       *core.Ctx
	scheduler *core.Scheduler
	commands  []core.Command
	snapshots []SnapshotRecord
	hashes    map[int]string
	interval  int
	keep      int
}

func New(cfg *config.Root, opts Options) *Engine {
	if opts.SnapshotInterval <= 0 {
		opts.SnapshotInterval = DefaultSnapshotInterval
	}
	if opts.SnapshotKeep <= 0 {
		opts.SnapshotKeep = DefaultSnapshotKeep
	}
	seed := uint64(opts.Seed)
	w := world.Gen(cfg, seed)
	core.SortCommands(opts.Commands)
	ctx := &core.Ctx{
		Seed: seed, Tick: w.Tick, Cfg: cfg,
		Ev: observe.NewEmitter(5000), Ledger: observe.NewLedger(),
		Metrics: observe.NewMetrics(20000), Commands: append([]core.Command(nil), opts.Commands...),
	}
	return &Engine{
		cfg: cfg, world: w, ctx: ctx, scheduler: systems.NewPipeline(),
		commands:  append([]core.Command(nil), opts.Commands...),
		snapshots: make([]SnapshotRecord, 0, opts.SnapshotKeep),
		hashes:    make(map[int]string), interval: opts.SnapshotInterval, keep: opts.SnapshotKeep,
	}
}

func (e *Engine) Step() {
	e.ctx.Ledger.BeginTick(e.world.Tick, systems.TotalEnergy(e.world))
	e.ctx.Tick = e.world.Tick
	e.scheduler.Run(e.world, e.ctx)
}

func (e *Engine) Run(ticks int, onTick func(*Engine) error) error {
	target := e.world.Tick + ticks
	for e.world.Tick < target {
		e.Step()
		if e.world.Tick%e.interval == 0 {
			e.TakeSnapshot()
		}
		if onTick != nil {
			if err := onTick(e); err != nil {
				return err
			}
		}
	}
	if ticks > 0 && e.world.Tick%e.interval != 0 {
		e.TakeSnapshot()
	}
	return nil
}

func (e *Engine) TakeSnapshot() SnapshotRecord {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(e.world); err != nil {
		panic(err)
	}
	rec := SnapshotRecord{Tick: e.world.Tick, Hash: observe.StateHash(e.world), Data: buf.Bytes()}
	e.snapshots = append(e.snapshots, rec)
	if len(e.snapshots) > e.keep {
		e.snapshots = e.snapshots[len(e.snapshots)-e.keep:]
	}
	e.hashes[rec.Tick] = fmt.Sprintf("%016x", rec.Hash)
	return rec
}

func (e *Engine) Snapshot(recentEvents, recentSamples int) *observe.Snapshot {
	return observe.SnapshotFromWorld(e.world, e.cfg, e.ctx.Ev.Recent(recentEvents), e.ctx.Metrics.Recent(recentSamples))
}

func (e *Engine) StateHash() uint64              { return observe.StateHash(e.world) }
func (e *Engine) Events() []observe.Event        { return e.ctx.Ev.Events() }
func (e *Engine) Metrics() []observe.Sample      { return e.ctx.Metrics.Samples() }
func (e *Engine) SnapshotHashes() map[int]string { return e.hashes }
func (e *Engine) Snapshots() []SnapshotRecord    { return append([]SnapshotRecord(nil), e.snapshots...) }
func (e *Engine) World() *world.World            { return e.world }
func (e *Engine) Ctx() *core.Ctx                 { return e.ctx }

type Tape struct {
	Seed         uint64         `json:"seed"`
	RulesVersion int            `json:"rules_version"`
	ConfigHash   string         `json:"config_hash"`
	Ticks        int            `json:"ticks"`
	Commands     []core.Command `json:"commands"`
	Snapshots    map[int]string `json:"snapshots"`
}

func (e *Engine) Tape(ticks int) Tape {
	return Tape{
		Seed: e.world.Seed, RulesVersion: e.cfg.Balance.RulesVersion,
		ConfigHash: e.cfg.Hash(), Ticks: ticks,
		Commands:  append([]core.Command(nil), e.commands...),
		Snapshots: e.SnapshotHashes(),
	}
}

func Replay(cfg *config.Root, tape Tape) error {
	if len(tape.Snapshots) == 0 {
		return fmt.Errorf("replay tape has no snapshot hashes")
	}
	if tape.RulesVersion != cfg.Balance.RulesVersion {
		return fmt.Errorf("rules version mismatch: tape=%d config=%d", tape.RulesVersion, cfg.Balance.RulesVersion)
	}
	if tape.ConfigHash != cfg.Hash() {
		return fmt.Errorf("config hash mismatch: tape=%s config=%s", tape.ConfigHash, cfg.Hash())
	}
	e := New(cfg, Options{Seed: tape.Seed, Commands: tape.Commands})
	for tick := 1; tick <= tape.Ticks; tick++ {
		e.Step()
		expected, ok := tape.Snapshots[e.world.Tick]
		if !ok {
			continue
		}
		actual := fmt.Sprintf("%016x", e.StateHash())
		if actual != expected {
			return fmt.Errorf("replay mismatch at tick %d: expected=%s actual=%s", e.world.Tick, expected, actual)
		}
	}
	return nil
}

func ParseTickKey(key string) (int, error) { return strconv.Atoi(key) }

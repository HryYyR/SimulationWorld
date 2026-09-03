package systems

import (
	"ecosim/internal/core"
)

func NewPipeline() *core.Scheduler {
	return core.NewScheduler(
		&ApplyCommands{},
		&AdvanceCalendar{},
		&StepClimate{},
		&ResolveModifiers{},
		&GrowGrass{},
		&DecayCorpses{},
		&HatchEggs{},
		&RebuildIndex{},
		&Behave{},
		&Metabolism{},
		&Reproduce{},
		&ResolveDeaths{},
		&LedgerMetrics{},
	)
}

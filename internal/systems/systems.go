package systems

import (
	"ecosim/internal/core"
)

func NewPipeline() *core.Scheduler {
	return core.NewScheduler(
		&ApplyCommands{},
		&AdvanceCalendar{},
		&StepWeather{},
		&ResolveModifiers{},
		&GrowGrass{},
		&DecayCorpses{},
		&RebuildIndex{},
		&Behave{},
		&Metabolism{},
		&Reproduce{},
		&ResolveDeaths{},
		&LedgerMetrics{},
	)
}

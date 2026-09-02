package systems

import (
	"ecosim/internal/core"
	"ecosim/internal/world"
)

type RebuildIndex struct{}

func (s *RebuildIndex) Name() string { return "rebuildIndex" }

func (s *RebuildIndex) Step(w *world.World, c *core.Ctx) {
	w.RebuildIndex()
	// 空间哈希索引（扁平数组分桶）：cell 大小取视野量级，半径查询只扫描相邻桶
	c.Index = world.NewSpatialHashIndex(w.Animals, 8, w.Grid.W, w.Grid.H)
}

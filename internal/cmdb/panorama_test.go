package cmdb

import (
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
)

func TestComputePanoramaHealth(t *testing.T) {
	edges := []model.TopologyEdge{
		{SourceID: "app", TargetID: "db", RelationType: "depends_on"},
	}
	criticalSet := map[string]bool{"db": true}

	mkNode := func() model.PanoramaNode { return model.PanoramaNode{} }

	cases := []struct {
		name string
		node model.PanoramaNode
		want string
	}{
		{
			name: "critical alert -> critical",
			node: func() model.PanoramaNode {
				n := mkNode()
				n.AlertSummary.Critical = 1
				return n
			}(),
			want: "critical",
		},
		{
			name: "root cause -> critical",
			node: func() model.PanoramaNode {
				n := mkNode()
				n.AlertSummary.RootCause = true
				return n
			}(),
			want: "critical",
		},
		{
			name: "warning alert -> warning",
			node: func() model.PanoramaNode {
				n := mkNode()
				n.AlertSummary.Warning = 2
				return n
			}(),
			want: "warning",
		},
		{
			name: "status down -> warning",
			node: func() model.PanoramaNode {
				n := mkNode()
				n.Status = "down"
				return n
			}(),
			want: "warning",
		},
		{
			name: "critical dependency upstream -> warning",
			node: func() model.PanoramaNode {
				n := mkNode()
				n.ID = "app"
				n.Status = "up"
				return n
			}(),
			want: "warning",
		},
		{
			name: "clean -> healthy",
			node: func() model.PanoramaNode {
				n := mkNode()
				n.ID = "web"
				n.Status = "up"
				return n
			}(),
			want: "healthy",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ComputePanoramaHealth(c.node, criticalSet, edges); got != c.want {
				t.Errorf("ComputePanoramaHealth = %q, want %q", got, c.want)
			}
		})
	}
}

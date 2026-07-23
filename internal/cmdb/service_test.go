package cmdb

import (
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// TestResolveChannel covers the channel-weight mapping used to pick the
// notification/escalation path for an incident (report issue 3.x).
func TestResolveChannel(t *testing.T) {
	cases := []struct {
		ch   int
		want model.ChannelWeight
	}{
		{95, model.WeightHuman},
		{80, model.WeightITSM},
		{50, model.WeightAutoScript},
		{10, model.WeightDiscovery},
	}
	for _, c := range cases {
		if got := resolveChannel(c.ch); got != c.want {
			t.Errorf("resolveChannel(%d) = %v, want %v", c.ch, got, c.want)
		}
	}
}

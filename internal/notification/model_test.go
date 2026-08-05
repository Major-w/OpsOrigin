package notification

import "testing"

func TestAllEventTypes(t *testing.T) {
	evs := AllEventTypes()
	if len(evs) == 0 {
		t.Fatal("expected non-empty event types")
	}
	want := map[EventType]bool{
		EventAlertTriggered: true, EventTicketCreated: true,
		EventTicketClosed: true, EventSLABreached: true,
		EventCMDBChanged: true, EventAIOncall: true,
	}
	for e := range want {
		found := false
		for _, x := range evs {
			if x == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing event type %q", e)
		}
	}
	seen := map[EventType]bool{}
	for _, e := range evs {
		if seen[e] {
			t.Errorf("duplicate event type %q", e)
		}
		seen[e] = true
	}
}

func TestAllChannelTypes(t *testing.T) {
	chs := AllChannelTypes()
	if len(chs) == 0 {
		t.Fatal("expected non-empty channel types")
	}
	want := map[ChannelType]bool{
		ChannelEmail: true, ChannelDingTalk: true, ChannelWeCom: true,
		ChannelFeishu: true, ChannelWebhook: true,
	}
	for c := range want {
		found := false
		for _, x := range chs {
			if x == c {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing channel type %q", c)
		}
	}
}

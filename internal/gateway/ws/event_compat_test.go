package ws

import "testing"

func TestCompatibilityEventAliasesMappings(t *testing.T) {
	cases := []struct {
		event string
		want  string
	}{
		{EventAgentStatus, EventCompatAgent},
		{EventAgentThinking, EventCompatAgent},
		{EventChatMessage, EventCompatChat},
		{EventChatChunk, EventCompatChat},
		{EventCronTick, EventCompatCron},
		{EventCronResult, EventCompatCron},
		{"presence.updated", EventCompatPresence},
		{EventTick, EventCompatHeartbeat},
		{EventVoicewake, EventCompatVoicewakeChanged},
	}
	for _, tc := range cases {
		aliases := compatibilityEventAliases(tc.event)
		if len(aliases) == 0 || aliases[0] != tc.want {
			t.Fatalf("event %q aliases=%v want first %q", tc.event, aliases, tc.want)
		}
	}
}

func TestAllPushEventsIncludesCompatAliases(t *testing.T) {
	has := func(name string) bool {
		for _, e := range AllPushEvents {
			if e == name {
				return true
			}
		}
		return false
	}
	for _, name := range []string{
		EventCompatAgent,
		EventCompatChat,
		EventCompatCron,
		EventCompatPresence,
		EventCompatHeartbeat,
		EventCompatVoicewakeChanged,
	} {
		if !has(name) {
			t.Fatalf("missing compat event %q in AllPushEvents", name)
		}
	}
}

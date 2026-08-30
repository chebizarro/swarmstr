package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"metiq/internal/gateway/channels"
)

func TestNostrGroupLoopControlTier2ProductionPath(t *testing.T) {
	msg := electionTestMsg(strings.Repeat("7", 64))
	msg.Text = "What do people think?"
	cfg := electionTestRoomCfg(map[string]any{
		"requireMention":    false,
		"responderElection": false,
	})
	lc := &nostrGroupLoopControl{ctx: context.Background(), ownPubkey: electAgentSelf}

	t.Run("ambiguous response verdict admits", func(t *testing.T) {
		called := 0
		unregister := channels.RegisterShouldReplyModelHookResolver(func(hookCtx channels.ShouldReplyModelHookContext) channels.ShouldReplyModelHook {
			if hookCtx.RoomKey != "nostr:room:relay.test'fleet-ops" {
				t.Fatalf("hook context = %+v", hookCtx)
			}
			return channels.ShouldReplyModelHookFunc(func(context.Context, channels.ShouldReplyModelInput) (channels.ShouldReplyModelVerdict, error) {
				called++
				return channels.ShouldReplyModelRespond, nil
			})
		})
		defer unregister()
		if _, admitted := lc.gate(msg, cfg, nil); !admitted {
			t.Fatal("RESPOND verdict did not reach dispatch")
		}
		if called != 1 {
			t.Fatalf("Tier 2 calls = %d, want 1", called)
		}
	})

	t.Run("provider error fails quiet", func(t *testing.T) {
		unregister := channels.RegisterShouldReplyModelHookResolver(func(channels.ShouldReplyModelHookContext) channels.ShouldReplyModelHook {
			return channels.ShouldReplyModelHookFunc(func(context.Context, channels.ShouldReplyModelInput) (channels.ShouldReplyModelVerdict, error) {
				return "", errors.New("provider down")
			})
		})
		defer unregister()
		if _, admitted := lc.gate(msg, cfg, nil); admitted {
			t.Fatal("provider error must fail quiet")
		}
	})

	t.Run("mention bypasses Tier 2", func(t *testing.T) {
		called := false
		unregister := channels.RegisterShouldReplyModelHookResolver(func(channels.ShouldReplyModelHookContext) channels.ShouldReplyModelHook {
			return channels.ShouldReplyModelHookFunc(func(context.Context, channels.ShouldReplyModelInput) (channels.ShouldReplyModelVerdict, error) {
				called = true
				return channels.ShouldReplyModelIgnore, nil
			})
		})
		defer unregister()
		mentioned := msg
		mentioned.Meta.MentionedPubkeys = []string{electAgentSelf}
		if _, admitted := lc.gate(mentioned, cfg, nil); !admitted {
			t.Fatal("mention must bypass Tier 2 and admit")
		}
		if called {
			t.Fatal("mention invoked Tier 2")
		}
	})
}

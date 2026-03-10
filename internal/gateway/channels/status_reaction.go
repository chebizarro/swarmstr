// Package channels status_reaction.go — automatic turn-lifecycle emoji reactions.
package channels

import (
	"context"
	"strings"
	"sync"
	"time"

	"swarmstr/internal/plugins/sdk"
)

// ─── Emoji constants ──────────────────────────────────────────────────────────

const (
	EmojiQueued    = "👀"
	EmojiThinking  = "🤔"
	EmojiToolWeb   = "🌐"
	EmojiToolCode  = "💻"
	EmojiToolFire  = "🔥"
	EmojiDone      = "👍"
	EmojiError     = "😱"
	EmojiStallSoft = "🥱"
	EmojiStallHard = "😨"
)

// tool name fragments used for emoji classification.
var webToolTokens = []string{"web", "search", "fetch", "http", "url", "browse"}
var codeToolTokens = []string{"bash", "shell", "exec", "code", "python", "script", "run"}

// classifyTool returns the emoji for the given tool name.
func classifyTool(toolName string) string {
	name := strings.ToLower(toolName)
	for _, tok := range webToolTokens {
		if strings.Contains(name, tok) {
			return EmojiToolWeb
		}
	}
	for _, tok := range codeToolTokens {
		if strings.Contains(name, tok) {
			return EmojiToolCode
		}
	}
	return EmojiToolFire
}

// ─── StatusReactionController ─────────────────────────────────────────────────

// StatusReactionController manages turn-lifecycle emoji reactions on a single
// inbound message. It serialises all reaction mutations through an internal
// goroutine to avoid races with debounce timers.
//
// Lifecycle:
//
//	ctrl := NewStatusReactionController(ctx, rh, eventID)
//	ctrl.SetQueued()      // 👀 immediately
//	ctrl.SetThinking()    // 🤔 after 700ms debounce
//	ctrl.SetTool("bash")  // 💻 immediately, clears previous emoji
//	ctrl.SetDone()        // 👍, removes current emoji, stops stall timers
//	ctrl.Close()          // always call to free resources
type StatusReactionController struct {
	mu      sync.Mutex
	rh      sdk.ReactionHandle
	eventID string
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// op channel serialises all state mutations.
	ops chan func()

	// current active emoji (so we can remove it before setting the next).
	current string

	// debounce timer for SetThinking.
	thinkDebounce *time.Timer

	// stall timers.
	stallSoft *time.Timer
	stallHard *time.Timer
}

const (
	thinkDebounceDelay = 700 * time.Millisecond
	stallSoftDelay     = 10 * time.Second
	stallHardDelay     = 30 * time.Second
)

// NewStatusReactionController creates and starts a controller.
// It does not add any reaction until one of the Set* methods is called.
func NewStatusReactionController(ctx context.Context, rh sdk.ReactionHandle, eventID string) *StatusReactionController {
	cctx, cancel := context.WithCancel(ctx)
	c := &StatusReactionController{
		rh:      rh,
		eventID: eventID,
		ctx:     cctx,
		cancel:  cancel,
		ops:     make(chan func(), 32),
	}
	c.wg.Add(1)
	go c.loop()
	return c
}

func (c *StatusReactionController) loop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.ctx.Done():
			c.drainAndClear()
			return
		case op, ok := <-c.ops:
			if !ok {
				return
			}
			op()
		}
	}
}

// drainAndClear drains remaining ops and removes current emoji.
func (c *StatusReactionController) drainAndClear() {
	for {
		select {
		case op := <-c.ops:
			op()
		default:
			c.removeCurrentLocked()
			return
		}
	}
}

func (c *StatusReactionController) enqueue(op func()) {
	select {
	case c.ops <- op:
	case <-c.ctx.Done():
	}
}

// setEmojiLocked swaps the current reaction emoji. Must run inside the loop goroutine.
func (c *StatusReactionController) setEmojiLocked(emoji string) {
	if c.current == emoji {
		return
	}
	prev := c.current
	c.current = emoji
	if prev != "" {
		_ = c.rh.RemoveReaction(c.ctx, c.eventID, prev)
	}
	if emoji != "" {
		_ = c.rh.AddReaction(c.ctx, c.eventID, emoji)
	}
}

func (c *StatusReactionController) removeCurrentLocked() {
	if c.current == "" {
		return
	}
	_ = c.rh.RemoveReaction(c.ctx, c.eventID, c.current)
	c.current = ""
}

func (c *StatusReactionController) cancelStalls() {
	if c.stallSoft != nil {
		c.stallSoft.Stop()
		c.stallSoft = nil
	}
	if c.stallHard != nil {
		c.stallHard.Stop()
		c.stallHard = nil
	}
}

func (c *StatusReactionController) startStallTimers() {
	c.cancelStalls()
	c.stallSoft = time.AfterFunc(stallSoftDelay, func() {
		c.enqueue(func() { c.setEmojiLocked(EmojiStallSoft) })
	})
	c.stallHard = time.AfterFunc(stallHardDelay, func() {
		c.enqueue(func() { c.setEmojiLocked(EmojiStallHard) })
	})
}

// ─── Public state setters ─────────────────────────────────────────────────────

// SetQueued adds the 👀 queued emoji immediately (no debounce).
func (c *StatusReactionController) SetQueued() {
	c.enqueue(func() {
		if c.thinkDebounce != nil {
			c.thinkDebounce.Stop()
			c.thinkDebounce = nil
		}
		c.setEmojiLocked(EmojiQueued)
	})
}

// SetThinking schedules the 🤔 thinking emoji after a 700ms debounce.
// This avoids flicker for very fast turns.
func (c *StatusReactionController) SetThinking() {
	c.enqueue(func() {
		if c.thinkDebounce != nil {
			c.thinkDebounce.Stop()
		}
		c.thinkDebounce = time.AfterFunc(thinkDebounceDelay, func() {
			c.enqueue(func() {
				c.setEmojiLocked(EmojiThinking)
				c.startStallTimers()
			})
		})
	})
}

// SetTool sets the emoji for the named tool immediately.
func (c *StatusReactionController) SetTool(toolName string) {
	emoji := classifyTool(toolName)
	c.enqueue(func() {
		if c.thinkDebounce != nil {
			c.thinkDebounce.Stop()
			c.thinkDebounce = nil
		}
		c.cancelStalls()
		c.setEmojiLocked(emoji)
		c.startStallTimers()
	})
}

// SetDone sets ✅ and stops all timers.
func (c *StatusReactionController) SetDone() {
	c.enqueue(func() {
		if c.thinkDebounce != nil {
			c.thinkDebounce.Stop()
			c.thinkDebounce = nil
		}
		c.cancelStalls()
		c.setEmojiLocked(EmojiDone)
	})
}

// SetError sets ❌ and stops all timers.
func (c *StatusReactionController) SetError() {
	c.enqueue(func() {
		if c.thinkDebounce != nil {
			c.thinkDebounce.Stop()
			c.thinkDebounce = nil
		}
		c.cancelStalls()
		c.setEmojiLocked(EmojiError)
	})
}

// Clear removes the current emoji without replacing it.
func (c *StatusReactionController) Clear() {
	c.enqueue(func() {
		if c.thinkDebounce != nil {
			c.thinkDebounce.Stop()
			c.thinkDebounce = nil
		}
		c.cancelStalls()
		c.removeCurrentLocked()
	})
}

// Close shuts down the controller and removes any active emoji.
func (c *StatusReactionController) Close() {
	c.cancel()
	c.wg.Wait()
}

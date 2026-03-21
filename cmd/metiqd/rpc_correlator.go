package main

// rpc_correlator.go — synchronous inter-agent RPC reply correlator.
//
// When nostr_agent_rpc sends a DM to a fleet agent and waits for a reply,
// it registers a pending expectation here via Register().  The shared inbound
// DM handler calls Deliver() for every accepted DM; if the sender has a
// pending waiter the reply is forwarded to it and the normal agent-turn
// pipeline is skipped for that message.
//
// Thread-safe.  Multiple callers may wait on different pubkeys simultaneously;
// only one waiter per pubkey at a time is supported (last-writer-wins on Register).

import (
	"sync"

	nostruntime "metiq/internal/nostr/runtime"
)

// rpcWaiter is a single pending reply expectation.
type rpcWaiter struct {
	ch     chan string
	cancel chan struct{}
}

// RPCCorrelator manages pending agent RPC reply waiters.
type RPCCorrelator struct {
	mu      sync.Mutex
	waiters map[string]*rpcWaiter // key: hex pubkey of expected sender
}

func newRPCCorrelator() *RPCCorrelator {
	return &RPCCorrelator{
		waiters: make(map[string]*rpcWaiter),
	}
}

// Register creates a pending waiter for a reply from fromPubkeyHex.
// Returns a channel that receives the reply text and a cancel func.
// The caller MUST call cancel() to clean up (defer is recommended).
// If a previous waiter exists for the same pubkey it is replaced.
func (c *RPCCorrelator) Register(fromPubkeyHex string) (<-chan string, func()) {
	// Normalize pubkey.
	pk, err := nostruntime.ParsePubKey(fromPubkeyHex)
	if err != nil {
		// Return a closed channel immediately — no valid pubkey to wait on.
		ch := make(chan string)
		close(ch)
		return ch, func() {}
	}
	hex := pk.Hex()

	ch := make(chan string, 1)
	done := make(chan struct{})

	w := &rpcWaiter{ch: ch, cancel: done}

	c.mu.Lock()
	// Cancel any previous waiter for this pubkey.
	if prev, ok := c.waiters[hex]; ok {
		select {
		case <-prev.cancel: // already closed
		default:
			close(prev.cancel)
		}
	}
	c.waiters[hex] = w
	c.mu.Unlock()

	cancel := func() {
		c.mu.Lock()
		if cur, ok := c.waiters[hex]; ok && cur == w {
			delete(c.waiters, hex)
		}
		c.mu.Unlock()
		select {
		case <-done:
		default:
			close(done)
		}
	}

	return ch, cancel
}

// Deliver attempts to deliver text to a pending waiter registered for fromPubkeyHex.
// Returns true if a waiter was found and the reply was delivered (caller should
// skip the normal agent-turn pipeline for this message).
func (c *RPCCorrelator) Deliver(fromPubkeyHex, text string) bool {
	pk, err := nostruntime.ParsePubKey(fromPubkeyHex)
	if err != nil {
		return false
	}
	hex := pk.Hex()

	c.mu.Lock()
	w, ok := c.waiters[hex]
	if ok {
		delete(c.waiters, hex)
	}
	c.mu.Unlock()

	if !ok {
		return false
	}

	// Non-blocking send: if the channel is already full (shouldn't happen
	// with buffer=1) we don't block the inbound DM pipeline.
	select {
	case w.ch <- text:
	default:
	}

	// Signal done so the waiter's cancel func knows the expectation is satisfied.
	select {
	case <-w.cancel:
	default:
		close(w.cancel)
	}

	return true
}

// WaiterFunc returns a toolbuiltin.RPCWaiter compatible closure backed by this correlator.
func (c *RPCCorrelator) WaiterFunc() func(string) (<-chan string, func()) {
	return func(fromPubkeyHex string) (<-chan string, func()) {
		return c.Register(fromPubkeyHex)
	}
}

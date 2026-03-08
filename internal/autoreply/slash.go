// Package autoreply provides slash command routing and per-session turn
// serialisation for swarmstrd's inbound message handler.
package autoreply

import (
	"context"
	"sort"
	"strings"
)

// Command is a parsed slash command extracted from an incoming message.
type Command struct {
	// Name is the command word without the leading slash, lower-cased.
	Name string
	// Args are the whitespace-separated tokens after the command name.
	Args []string
	// RawText is the original, unmodified message text.
	RawText string
	// SessionID identifies the conversation/session (usually the sender pubkey).
	SessionID string
	// FromPubKey is the sender's Nostr public key.
	FromPubKey string
}

// Handler processes a slash command and returns a reply string.
// Returning a non-nil error causes the router to reply with a generic
// error message; the error itself is logged by the caller.
type Handler func(ctx context.Context, cmd Command) (string, error)

// Router dispatches "/" prefixed messages to registered Handler functions.
// It is safe for concurrent use after all Register calls have completed at
// startup (Register itself is NOT goroutine-safe).
type Router struct {
	handlers map[string]Handler
}

// NewRouter creates an empty Router.
func NewRouter() *Router {
	return &Router{handlers: make(map[string]Handler)}
}

// Register adds a handler for the given command name (without "/").
// Calling Register after the daemon has started handling messages is not
// safe; all registrations should happen during initialisation.
func (r *Router) Register(name string, h Handler) {
	r.handlers[strings.ToLower(strings.TrimPrefix(name, "/"))] = h
}

// Registered returns a sorted list of all registered command names.
func (r *Router) Registered() []string {
	names := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Parse attempts to parse a slash command from text.
// It returns nil if text does not start with "/".
func Parse(text string) *Command {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return nil
	}
	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return nil
	}
	name := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	if name == "" {
		return nil
	}
	var args []string
	if len(parts) > 1 {
		args = parts[1:]
	}
	return &Command{
		Name:    name,
		Args:    args,
		RawText: trimmed,
	}
}

// Dispatch tries to handle cmd using a registered handler.
// It returns (reply, true, err) when a handler is found and invoked,
// or ("", false, nil) when the command name is not registered.
func (r *Router) Dispatch(ctx context.Context, cmd *Command) (reply string, handled bool, err error) {
	if cmd == nil {
		return "", false, nil
	}
	h, ok := r.handlers[cmd.Name]
	if !ok {
		return "", false, nil
	}
	reply, err = h(ctx, *cmd)
	return reply, true, err
}

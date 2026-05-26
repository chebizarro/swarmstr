// Package rules provides a dynamic, markdown-backed policy rule engine for
// user and team guardrails. It intentionally evaluates generic events and Nostr
// metadata only; runtime per-tool permission enforcement lives elsewhere.
package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type Action string

const (
	ActionAllow Action = "allow"
	ActionWarn  Action = "warn"
	ActionBlock Action = "block"
)

type EventType string

const (
	EventTool   EventType = "tool"
	EventBash   EventType = "bash"
	EventFile   EventType = "file"
	EventStop   EventType = "stop"
	EventPrompt EventType = "prompt"
	EventNostr  EventType = "nostr"
)

type Rule struct {
	ID          string        `yaml:"id" json:"id"`
	Name        string        `yaml:"name" json:"name"`
	Description string        `yaml:"description" json:"description,omitempty"`
	Enabled     *bool         `yaml:"enabled" json:"enabled,omitempty"`
	Action      Action        `yaml:"action" json:"action"`
	EventTypes  []EventType   `yaml:"event_types" json:"event_types,omitempty"`
	Message     string        `yaml:"message" json:"message,omitempty"`
	Conditions  []Condition   `yaml:"conditions" json:"conditions,omitempty"`
	Nostr       *NostrMatcher `yaml:"nostr" json:"nostr,omitempty"`
	Source      string        `yaml:"-" json:"source,omitempty"`
	Body        string        `yaml:"-" json:"-"`
}

type Condition struct {
	Field       string   `yaml:"field" json:"field"`
	Equals      string   `yaml:"equals" json:"equals,omitempty"`
	Contains    string   `yaml:"contains" json:"contains,omitempty"`
	Regex       string   `yaml:"regex" json:"regex,omitempty"`
	AnyContains []string `yaml:"any_contains" json:"any_contains,omitempty"`
}

type NostrMatcher struct {
	Kinds         []int               `yaml:"kinds" json:"kinds,omitempty"`
	RelayURLs     []string            `yaml:"relay_urls" json:"relay_urls,omitempty"`
	Tags          map[string][]string `yaml:"tags" json:"tags,omitempty"`
	RequiredTags  []string            `yaml:"required_tags" json:"required_tags,omitempty"`
	FilterHashtag string              `yaml:"filter_hashtag" json:"filter_hashtag,omitempty"`
}

type Event struct {
	Type       EventType         `json:"type"`
	ToolName   string            `json:"tool_name,omitempty"`
	Command    string            `json:"command,omitempty"`
	FilePath   string            `json:"file_path,omitempty"`
	Prompt     string            `json:"prompt,omitempty"`
	Content    string            `json:"content,omitempty"`
	Nostr      NostrEventContext `json:"nostr,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type NostrEventContext struct {
	Kind     int                 `json:"kind,omitempty"`
	RelayURL string              `json:"relay_url,omitempty"`
	Tags     map[string][]string `json:"tags,omitempty"`
	Filter   map[string]any      `json:"filter,omitempty"`
}

type Decision struct {
	Action  Action `json:"action"`
	RuleID  string `json:"rule_id,omitempty"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message,omitempty"`
	Source  string `json:"source,omitempty"`
	Matched bool   `json:"matched"`
}

type Engine struct {
	Rules []Rule
}

func New(ruleSet []Rule) (*Engine, error) {
	normalized := make([]Rule, 0, len(ruleSet))
	for _, r := range ruleSet {
		nr, err := normalizeRule(r)
		if err != nil {
			return nil, err
		}
		if nr.isEnabled() {
			normalized = append(normalized, nr)
		}
	}
	return &Engine{Rules: normalized}, nil
}

func (e *Engine) Evaluate(ev Event) Decision {
	if e == nil {
		return Decision{Action: ActionAllow}
	}
	best := Decision{Action: ActionAllow}
	for _, rule := range e.Rules {
		if !rule.matches(ev) {
			continue
		}
		d := Decision{Action: rule.Action, RuleID: rule.ID, Name: rule.Name, Message: rule.Message, Source: rule.Source, Matched: true}
		if d.Message == "" {
			d.Message = rule.Description
		}
		if rule.Action == ActionBlock {
			return d
		}
		if rule.Action == ActionWarn && best.Action != ActionWarn {
			best = d
		}
	}
	return best
}

func (r Rule) matches(ev Event) bool {
	if len(r.EventTypes) > 0 {
		ok := false
		for _, t := range r.EventTypes {
			if t == ev.Type {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if r.Nostr != nil && !r.Nostr.matches(ev.Nostr) {
		return false
	}
	for _, c := range r.Conditions {
		if !c.matches(ev) {
			return false
		}
	}
	return true
}

func (n NostrMatcher) matches(ctx NostrEventContext) bool {
	if len(n.Kinds) > 0 {
		ok := false
		for _, k := range n.Kinds {
			if k == ctx.Kind {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(n.RelayURLs) > 0 {
		ok := false
		for _, u := range n.RelayURLs {
			if strings.EqualFold(u, ctx.RelayURL) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, tag := range n.RequiredTags {
		if len(ctx.Tags[tag]) == 0 {
			return false
		}
	}
	for tag, allowed := range n.Tags {
		vals := ctx.Tags[tag]
		if len(vals) == 0 {
			return false
		}
		if len(allowed) == 0 {
			continue
		}
		matched := false
		for _, v := range vals {
			for _, a := range allowed {
				if v == a {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			return false
		}
	}
	if n.FilterHashtag != "" {
		raw, ok := ctx.Filter["#t"]
		if !ok {
			return false
		}
		if !filterContains(raw, n.FilterHashtag) {
			return false
		}
	}
	return true
}

func filterContains(raw any, want string) bool {
	switch v := raw.(type) {
	case []string:
		for _, s := range v {
			if s == want {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if fmt.Sprint(item) == want {
				return true
			}
		}
	case string:
		return v == want
	}
	return false
}

func (c Condition) matches(ev Event) bool {
	value := fieldValue(ev, c.Field)
	if c.Equals != "" && value != c.Equals {
		return false
	}
	if c.Contains != "" && !strings.Contains(value, c.Contains) {
		return false
	}
	if len(c.AnyContains) > 0 {
		ok := false
		for _, needle := range c.AnyContains {
			if strings.Contains(value, needle) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if c.Regex != "" {
		re, err := regexp.Compile(c.Regex)
		if err != nil || !re.MatchString(value) {
			return false
		}
	}
	return true
}

func fieldValue(ev Event, field string) string {
	switch strings.ToLower(field) {
	case "tool", "tool_name":
		return ev.ToolName
	case "command", "cmd":
		return ev.Command
	case "file", "path", "file_path":
		return ev.FilePath
	case "prompt":
		return ev.Prompt
	case "content", "text":
		return ev.Content
	case "relay", "relay_url":
		return ev.Nostr.RelayURL
	case "kind", "nostr.kind":
		return fmt.Sprint(ev.Nostr.Kind)
	default:
		if strings.HasPrefix(field, "attr.") {
			return ev.Attributes[strings.TrimPrefix(field, "attr.")]
		}
		return ""
	}
}

func normalizeRule(r Rule) (Rule, error) {
	if r.ID == "" {
		r.ID = strings.TrimSpace(strings.ToLower(strings.ReplaceAll(r.Name, " ", "-")))
	}
	if r.ID == "" {
		return r, errors.New("rule requires id or name")
	}
	if r.Action == "" {
		r.Action = ActionWarn
	}
	switch r.Action {
	case ActionAllow, ActionWarn, ActionBlock:
	default:
		return r, fmt.Errorf("rule %s invalid action %q", r.ID, r.Action)
	}
	for _, c := range r.Conditions {
		if c.Regex == "" {
			continue
		}
		if _, err := regexp.Compile(c.Regex); err != nil {
			return r, fmt.Errorf("rule %s invalid regex for field %s: %w", r.ID, c.Field, err)
		}
	}
	return r, nil
}

func (r Rule) isEnabled() bool { return r.Enabled == nil || *r.Enabled }

func LoadFile(path string) (Rule, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Rule{}, err
	}
	rule, err := ParseMarkdownRule(string(raw))
	if err != nil {
		return Rule{}, fmt.Errorf("%s: %w", path, err)
	}
	rule.Source = path
	return normalizeRule(rule)
}

func ParseMarkdownRule(src string) (Rule, error) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	if !strings.HasPrefix(src, "---\n") {
		return Rule{}, errors.New("rule markdown requires YAML frontmatter")
	}
	rest := src[len("---\n"):]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return Rule{}, errors.New("frontmatter is not closed")
	}
	front := rest[:idx]
	body := strings.TrimSpace(rest[idx+len("\n---"):])
	var rule Rule
	if err := yaml.Unmarshal([]byte(front), &rule); err != nil {
		return Rule{}, err
	}
	rule.Body = body
	return normalizeRule(rule)
}

func LoadDirectories(root string) ([]Rule, error) {
	var paths []string
	for _, pattern := range []string{
		filepath.Join(root, ".metiq", "*.local.md"),
		filepath.Join(root, ".claude", "metiq.*.local.md"),
	} {
		found, _ := filepath.Glob(pattern)
		paths = append(paths, found...)
	}
	sort.Strings(paths)
	out := make([]Rule, 0, len(paths))
	for _, p := range paths {
		r, err := LoadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

type Store struct {
	mu      sync.RWMutex
	root    string
	engine  *Engine
	modHash string
}

func NewStore(root string) (*Store, error) {
	s := &Store{root: root}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Reload() error {
	rules, err := LoadDirectories(s.root)
	if err != nil {
		return err
	}
	builtin, err := BuiltinRulePacks()
	if err != nil {
		return err
	}
	rules = append(builtin, rules...)
	engine, err := New(rules)
	if err != nil {
		return err
	}
	hash, _ := s.currentHash()
	s.mu.Lock()
	s.engine = engine
	s.modHash = hash
	s.mu.Unlock()
	return nil
}

func (s *Store) ReloadIfChanged() (bool, error) {
	hash, err := s.currentHash()
	if err != nil {
		return false, err
	}
	s.mu.RLock()
	old := s.modHash
	s.mu.RUnlock()
	if hash == old {
		return false, nil
	}
	return true, s.Reload()
}

func (s *Store) Evaluate(ev Event) Decision {
	s.mu.RLock()
	e := s.engine
	s.mu.RUnlock()
	if e == nil {
		return Decision{Action: ActionAllow}
	}
	return e.Evaluate(ev)
}

func (s *Store) currentHash() (string, error) {
	rules, _ := LoadDirectories(s.root)
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.Source == "" {
			continue
		}
		info, err := os.Stat(r.Source)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d", r.Source, info.Size(), info.ModTime().UnixNano()))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return fmt.Sprint(time.Unix(0, 0).UnixNano()), nil
	}
	return strings.Join(parts, "|"), nil
}

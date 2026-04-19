// Package toolbuiltin/tool_chain provides composable multi-tool chains (macros):
//   - chain_define → create/update a named chain of tool steps
//   - chain_run    → execute a chain with parameter expansion
//   - chain_list   → list available chains
//
// Steps execute sequentially; each step's output is available to later steps
// via {{steps.NAME}} templates. Chain-level parameters are {{params.NAME}}.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
)

// ─── Constants ──────────────────────────────────────────────────────────────

const (
	maxChainSteps    = 20              // max steps per chain definition
	maxChainDepth    = 3               // max recursive chain-in-chain calls
	chainExecTimeout = 120 * time.Second
	chainOutputMax   = 2000            // per-step output chars in summary
)

// chainDepthKey is the context key for tracking recursive chain depth.
type chainDepthKey struct{}

// ─── Chain types ────────────────────────────────────────────────────────────

// ChainStep defines a single step in a tool chain.
type ChainStep struct {
	Name   string         `json:"name"`              // identifier for referencing output
	Tool   string         `json:"tool"`              // tool to invoke
	Args   map[string]any `json:"args,omitempty"`    // may contain {{params.X}} or {{steps.Y}}
	OnFail string         `json:"on_fail,omitempty"` // "stop" (default), "skip", "continue"
}

// ChainDef is a named, reusable multi-tool workflow.
type ChainDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Steps       []ChainStep `json:"steps"`
}

type chainStepResult struct {
	Step   string `json:"step"`
	Tool   string `json:"tool"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
	Status string `json:"status"` // "ok", "failed", "skipped"
}

// ─── Chain registry ─────────────────────────────────────────────────────────

// ChainRegistry manages named tool chains. It is safe for concurrent use.
type ChainRegistry struct {
	mu     sync.RWMutex
	chains map[string]*ChainDef
}

// NewChainRegistry creates an empty chain registry.
func NewChainRegistry() *ChainRegistry {
	return &ChainRegistry{chains: make(map[string]*ChainDef)}
}

func (r *ChainRegistry) define(def ChainDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chains[def.Name] = &def
}

func (r *ChainRegistry) get(name string) (*ChainDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.chains[name]
	if !ok {
		return nil, false
	}
	cp := *def
	cp.Steps = append([]ChainStep(nil), def.Steps...)
	return &cp, true
}

func (r *ChainRegistry) list() []ChainDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ChainDef, 0, len(r.chains))
	for _, def := range r.chains {
		out = append(out, *def)
	}
	return out
}

func (r *ChainRegistry) remove(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.chains[name]; ok {
		delete(r.chains, name)
		return true
	}
	return false
}

// ─── Template expansion ─────────────────────────────────────────────────────

var chainTemplateRe = regexp.MustCompile(`\{\{(params|steps)\.([a-zA-Z0-9_]+)\}\}`)

func chainExpandTemplates(s string, params map[string]any, stepOutputs map[string]string) string {
	return chainTemplateRe.ReplaceAllStringFunc(s, func(match string) string {
		parts := chainTemplateRe.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		switch parts[1] {
		case "params":
			if v, ok := params[parts[2]]; ok {
				return fmt.Sprintf("%v", v)
			}
		case "steps":
			if v, ok := stepOutputs[parts[2]]; ok {
				return v
			}
		}
		return match // leave unresolved templates as-is
	})
}

func chainExpandArgs(args map[string]any, params map[string]any, stepOutputs map[string]string) map[string]any {
	if len(args) == 0 {
		return map[string]any{}
	}
	result := make(map[string]any, len(args))
	for k, v := range args {
		result[k] = chainExpandValue(v, params, stepOutputs)
	}
	return result
}

func chainExpandValue(v any, params map[string]any, stepOutputs map[string]string) any {
	switch t := v.(type) {
	case string:
		return chainExpandTemplates(t, params, stepOutputs)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = chainExpandValue(val, params, stepOutputs)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = chainExpandValue(val, params, stepOutputs)
		}
		return out
	default:
		return v
	}
}

// ─── Chain execution ────────────────────────────────────────────────────────

func executeChain(ctx context.Context, toolReg *agent.ToolRegistry, chain *ChainDef, params map[string]any) ([]chainStepResult, error) {
	depth := 0
	if v, ok := ctx.Value(chainDepthKey{}).(int); ok {
		depth = v
	}
	if depth >= maxChainDepth {
		return nil, fmt.Errorf("chain recursion limit reached (max depth %d)", maxChainDepth)
	}
	ctx = context.WithValue(ctx, chainDepthKey{}, depth+1)

	ctx, cancel := context.WithTimeout(ctx, chainTimeout(ctx))
	defer cancel()

	stepOutputs := make(map[string]string)
	results := make([]chainStepResult, 0, len(chain.Steps))

	for i, step := range chain.Steps {
		if ctx.Err() != nil {
			return results, fmt.Errorf("chain timed out at step %d/%d", i+1, len(chain.Steps))
		}

		expanded := chainExpandArgs(step.Args, params, stepOutputs)
		stepName := step.Name
		if stepName == "" {
			stepName = fmt.Sprintf("step_%d", i)
		}

		call := agent.ToolCall{
			Name: step.Tool,
			Args: expanded,
		}

		output, err := toolReg.Execute(ctx, call)

		res := chainStepResult{Step: stepName, Tool: step.Tool}

		if err != nil {
			res.Error = err.Error()
			onFail := step.OnFail
			if onFail == "" {
				onFail = "stop"
			}
			switch onFail {
			case "stop":
				res.Status = "failed"
				results = append(results, res)
				return results, fmt.Errorf("stopped at step %q (%s): %v", stepName, step.Tool, err)
			case "skip":
				res.Status = "skipped"
			case "continue":
				res.Status = "failed"
				stepOutputs[stepName] = ""
			}
		} else {
			res.Output = output
			res.Status = "ok"
			stepOutputs[stepName] = output
		}

		results = append(results, res)
	}

	return results, nil
}

// ─── Result formatting ──────────────────────────────────────────────────────

func formatChainResult(chainName string, results []chainStepResult, chainErr error) string {
	var b strings.Builder

	okCount := 0
	for _, r := range results {
		if r.Status == "ok" {
			okCount++
		}
	}

	total := len(results)
	if chainErr != nil {
		fmt.Fprintf(&b, "Chain %q failed (%d/%d steps completed)\n\n", chainName, okCount, total)
	} else {
		fmt.Fprintf(&b, "Chain %q completed (%d/%d steps ok)\n\n", chainName, okCount, total)
	}

	for i, r := range results {
		fmt.Fprintf(&b, "── step %d: %s (%s) ── %s\n", i+1, r.Step, r.Tool, r.Status)
		if r.Output != "" {
			out := r.Output
			if len(out) > chainOutputMax {
				out = out[:chainOutputMax] + fmt.Sprintf("\n... [truncated, %d chars total]", len(r.Output))
			}
			b.WriteString(out)
			if !strings.HasSuffix(out, "\n") {
				b.WriteByte('\n')
			}
		}
		if r.Error != "" {
			fmt.Fprintf(&b, "error: %s\n", r.Error)
		}
		b.WriteByte('\n')
	}

	if chainErr != nil {
		fmt.Fprintf(&b, "⚠️ %s\n", chainErr)
	}

	return b.String()
}

// ─── Validation helpers ─────────────────────────────────────────────────────

func parseChainSteps(stepsJSON string) ([]ChainStep, error) {
	var steps []ChainStep
	if err := json.Unmarshal([]byte(stepsJSON), &steps); err != nil {
		return nil, fmt.Errorf("invalid steps_json: %w", err)
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("chain must have at least one step")
	}
	if len(steps) > maxChainSteps {
		return nil, fmt.Errorf("chain exceeds maximum of %d steps", maxChainSteps)
	}

	for i := range steps {
		if steps[i].Tool == "" {
			return nil, fmt.Errorf("step %d: tool is required", i)
		}
		if steps[i].Name == "" {
			steps[i].Name = fmt.Sprintf("step_%d", i)
		}
		switch steps[i].OnFail {
		case "", "stop", "skip", "continue":
			// ok
		default:
			return nil, fmt.Errorf("step %d: on_fail must be stop, skip, or continue", i)
		}
	}
	return steps, nil
}

// ─── Tool definitions ───────────────────────────────────────────────────────

// ChainDefineDef is the ToolDefinition for chain_define.
var ChainDefineDef = agent.ToolDefinition{
	Name: "chain_define",
	Description: `Define a reusable multi-tool chain (macro). Steps execute sequentially; each step's output is available to later steps via {{steps.NAME}} templates. Chain parameters are referenced as {{params.NAME}}.

Example steps_json:
[
  {"name":"find","tool":"grep_search","args":{"pattern":"{{params.query}}","path":"{{params.dir}}"}},
  {"name":"read","tool":"read_file","args":{"path":"{{params.file}}"},"on_fail":"skip"}
]

on_fail options: "stop" (default — abort chain), "skip" (skip step, proceed), "continue" (record error, proceed).`,
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"name": {
				Type:        "string",
				Description: "Unique chain name (snake_case). Re-defining overwrites.",
			},
			"description": {
				Type:        "string",
				Description: "Human-readable description of what this chain does.",
			},
			"steps_json": {
				Type:        "string",
				Description: `JSON array of step objects. Each step: {"name":"…","tool":"…","args":{…},"on_fail":"stop|skip|continue"}.`,
			},
		},
		Required: []string{"name", "steps_json"},
	},
}

// ChainRunDef is the ToolDefinition for chain_run.
var ChainRunDef = agent.ToolDefinition{
	Name: "chain_run",
	Description: `Execute a previously defined tool chain by name. Parameters fill {{params.NAME}} templates in step args. Step outputs are forwarded to subsequent steps via {{steps.NAME}}.

Example: {"chain":"search_and_read","params_json":"{\"query\":\"TODO\",\"dir\":\"src/\"}"}`,
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"chain": {
				Type:        "string",
				Description: "Name of the chain to execute.",
			},
			"params_json": {
				Type:        "string",
				Description: "JSON object of parameter values for template expansion. Optional if the chain has no {{params.X}} references.",
			},
		},
		Required: []string{"chain"},
	},
}

// ChainListDef is the ToolDefinition for chain_list.
var ChainListDef = agent.ToolDefinition{
	Name:        "chain_list",
	Description: "List all defined tool chains with their descriptions and step summaries.",
	Parameters:  agent.ToolParameters{Type: "object"},
}

// ─── Tool implementations ───────────────────────────────────────────────────

// ChainDefineTool returns the chain_define tool function.
func ChainDefineTool(reg *ChainRegistry) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		name := strings.TrimSpace(agent.ArgString(args, "name"))
		if name == "" {
			return "", fmt.Errorf("name is required")
		}

		stepsJSON := agent.ArgString(args, "steps_json")
		if stepsJSON == "" {
			return "", fmt.Errorf("steps_json is required")
		}

		steps, err := parseChainSteps(stepsJSON)
		if err != nil {
			return "", err
		}

		def := ChainDef{
			Name:        name,
			Description: strings.TrimSpace(agent.ArgString(args, "description")),
			Steps:       steps,
		}
		reg.define(def)

		var b strings.Builder
		fmt.Fprintf(&b, "Chain %q defined with %d step(s):\n", name, len(steps))
		for i, s := range steps {
			onFail := s.OnFail
			if onFail == "" {
				onFail = "stop"
			}
			fmt.Fprintf(&b, "  %d. %s → %s (on_fail=%s)\n", i+1, s.Name, s.Tool, onFail)
		}
		return b.String(), nil
	}
}

// ChainRunTool returns the chain_run tool function.
func ChainRunTool(reg *ChainRegistry, toolReg *agent.ToolRegistry) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		chainName := strings.TrimSpace(agent.ArgString(args, "chain"))
		if chainName == "" {
			return "", fmt.Errorf("chain name is required")
		}

		chain, ok := reg.get(chainName)
		if !ok {
			available := reg.list()
			if len(available) == 0 {
				return "", fmt.Errorf("unknown chain %q — no chains defined yet (use chain_define first)", chainName)
			}
			names := make([]string, len(available))
			for i, c := range available {
				names[i] = c.Name
			}
			return "", fmt.Errorf("unknown chain %q (available: %s)", chainName, strings.Join(names, ", "))
		}

		var params map[string]any
		if pj := agent.ArgString(args, "params_json"); pj != "" {
			if err := json.Unmarshal([]byte(pj), &params); err != nil {
				return "", fmt.Errorf("invalid params_json: %w", err)
			}
		}
		if params == nil {
			params = map[string]any{}
		}

		results, chainErr := executeChain(ctx, toolReg, chain, params)
		// Always return formatted result — chain failures are reported as content,
		// not as Go errors, so the LLM sees the step-by-step outcome.
		return formatChainResult(chainName, results, chainErr), nil
	}
}

// ChainListTool returns the chain_list tool function.
func ChainListTool(reg *ChainRegistry) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		chains := reg.list()
		if len(chains) == 0 {
			return "No chains defined. Use chain_define to create one.", nil
		}

		var b strings.Builder
		fmt.Fprintf(&b, "%d chain(s) defined:\n\n", len(chains))
		for _, c := range chains {
			fmt.Fprintf(&b, "• %s", c.Name)
			if c.Description != "" {
				fmt.Fprintf(&b, " — %s", c.Description)
			}
			fmt.Fprintf(&b, " (%d steps)\n", len(c.Steps))
			for i, s := range c.Steps {
				onFail := s.OnFail
				if onFail == "" {
					onFail = "stop"
				}
				fmt.Fprintf(&b, "    %d. %s → %s (on_fail=%s)\n", i+1, s.Name, s.Tool, onFail)
			}
		}
		return b.String(), nil
	}
}

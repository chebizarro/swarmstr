// Command paritygen regenerates the checked-in OpenClaw parity catalogs.
//
// It intentionally reads OpenClaw's TypeScript descriptor catalogs as text:
// the descriptors are simple static object arrays and no Node toolchain is
// required to refresh or verify the snapshots.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"metiq/internal/gateway/methods"
)

const (
	gatewayDescriptorPath = "src/gateway/methods/core-descriptors.ts"
	coreCLIDescriptorPath = "src/cli/program/core-command-descriptors.ts"
	subCLIDescriptorPath  = "src/cli/program/subcli-descriptors.ts"
)

var (
	arrayAssignmentRE = regexp.MustCompile(`=\s*\[`)
	nameRE            = regexp.MustCompile(`name:\s*"([^"]+)"`)
)

type descriptor struct {
	Name       string
	Advertised bool
}

type sourceMetadata struct {
	Project  string            `json:"project"`
	Revision string            `json:"revision"`
	Files    map[string]string `json:"files"`
}

type triageConfig struct {
	SchemaVersion  int               `json:"schema_version"`
	CategoryValues []string          `json:"category_values"`
	Groups         []triageGroup     `json:"groups"`
	MethodNotes    map[string]string `json:"method_notes"`
}

type triageGroup struct {
	ID         string   `json:"id"`
	Prefixes   []string `json:"prefixes"`
	Category   string   `json:"category"`
	Workstream string   `json:"workstream"`
	Rationale  string   `json:"rationale"`
}

type gatewaySnapshot struct {
	SchemaVersion int            `json:"schema_version"`
	Source        sourceMetadata `json:"source"`
	CapturedAt    string         `json:"captured_at"`
	Summary       gatewaySummary `json:"summary"`
	Entries       []gatewayEntry `json:"entries"`
}

type gatewaySummary struct {
	OpenClawDefinedCount int            `json:"openclaw_defined_count"`
	OpenClawMethodCount  int            `json:"openclaw_method_count"`
	HiddenMethodCount    int            `json:"hidden_method_count"`
	Implemented          int            `json:"implemented"`
	Partial              int            `json:"partial"`
	Missing              int            `json:"missing"`
	TriageCategoryCounts map[string]int `json:"triage_category_counts"`
}

type gatewayEntry struct {
	Method      string `json:"method"`
	Status      string `json:"status"`
	MetiqMethod string `json:"metiq_method,omitempty"`
	Notes       string `json:"notes"`
	Triage      string `json:"triage"`
	TriageGroup string `json:"triage_group"`
	Workstream  string `json:"workstream"`
}

type cliClassifications struct {
	SchemaVersion   int                          `json:"schema_version"`
	StatusValues    []string                     `json:"status_values"`
	Classifications map[string]cliClassification `json:"classifications"`
}

type cliClassification struct {
	Status       string `json:"status"`
	MetiqCommand string `json:"metiq_command,omitempty"`
	MetiqEntry   string `json:"metiq_entry,omitempty"`
	Rationale    string `json:"rationale,omitempty"`
}

type cliSnapshot struct {
	SchemaVersion int            `json:"schema_version"`
	GeneratedFor  string         `json:"generated_for"`
	Reference     sourceMetadata `json:"reference"`
	CapturedAt    string         `json:"captured_at"`
	StatusValues  []string       `json:"status_values"`
	Summary       cliSummary     `json:"summary"`
	Groups        []cliEntry     `json:"groups"`
}

type cliSummary struct {
	OpenClawCommandCount int            `json:"openclaw_command_count"`
	StatusCounts         map[string]int `json:"status_counts"`
}

type cliEntry struct {
	Name            string `json:"name"`
	ReferenceSource string `json:"reference_source"`
	Status          string `json:"status"`
	MetiqCommand    string `json:"metiq_command,omitempty"`
	MetiqEntry      string `json:"metiq_entry,omitempty"`
	Rationale       string `json:"rationale,omitempty"`
}

type coreDeviationDoc struct {
	AcceptedDeviations []map[string]string `json:"accepted_deviations"`
}

type coreDeviationFixture struct {
	Source                    string              `json:"source"`
	CapturedAt                string              `json:"captured_at"`
	AcceptedMissingMethods    []string            `json:"accepted_missing_methods"`
	AcceptedAdditionalMethods []string            `json:"accepted_additional_methods"`
	AcceptedDeviations        []map[string]string `json:"accepted_deviations"`
}

type outputFile struct {
	Path string
	Data []byte
}

func main() {
	var (
		openclawRoot = flag.String("openclaw-root", "", "path to an OpenClaw checkout")
		repoRoot     = flag.String("repo-root", ".", "path to the swarmstr checkout")
		check        = flag.Bool("check", false, "verify generated files without writing")
	)
	flag.Parse()

	if strings.TrimSpace(*openclawRoot) == "" {
		fatal(errors.New("--openclaw-root is required (or use scripts/refresh-parity.sh)"))
	}

	files, err := generate(filepath.Clean(*repoRoot), filepath.Clean(*openclawRoot))
	if err != nil {
		fatal(err)
	}
	for _, file := range files {
		if *check {
			current, err := os.ReadFile(file.Path)
			if err != nil {
				fatal(fmt.Errorf("read generated file %s: %w", file.Path, err))
			}
			if !bytes.Equal(current, file.Data) {
				fatal(fmt.Errorf("%s is stale; run scripts/refresh-parity.sh", file.Path))
			}
			continue
		}
		if err := os.WriteFile(file.Path, file.Data, 0o644); err != nil {
			fatal(fmt.Errorf("write %s: %w", file.Path, err))
		}
		fmt.Printf("updated %s\n", file.Path)
	}
}

func generate(repoRoot, openclawRoot string) ([]outputFile, error) {
	gatewayRaw, err := os.ReadFile(filepath.Join(openclawRoot, gatewayDescriptorPath))
	if err != nil {
		return nil, fmt.Errorf("read gateway descriptors: %w", err)
	}
	coreCLIRaw, err := os.ReadFile(filepath.Join(openclawRoot, coreCLIDescriptorPath))
	if err != nil {
		return nil, fmt.Errorf("read core CLI descriptors: %w", err)
	}
	subCLIRaw, err := os.ReadFile(filepath.Join(openclawRoot, subCLIDescriptorPath))
	if err != nil {
		return nil, fmt.Errorf("read sub-CLI descriptors: %w", err)
	}

	gatewayDescriptors, err := parseDescriptorArray(gatewayRaw, "const CORE_GATEWAY_METHOD_SPECS", true)
	if err != nil {
		return nil, fmt.Errorf("parse gateway descriptors: %w", err)
	}
	coreCLI, err := parseDescriptorArray(coreCLIRaw, "const coreCliCommandCatalog", false)
	if err != nil {
		return nil, fmt.Errorf("parse core CLI descriptors: %w", err)
	}
	subCLI, err := parseDescriptorArray(subCLIRaw, "const subCliCommandCatalog", false)
	if err != nil {
		return nil, fmt.Errorf("parse sub-CLI descriptors: %w", err)
	}

	revision, capturedAt, err := gitMetadata(openclawRoot)
	if err != nil {
		return nil, err
	}
	gatewaySource := sourceMetadata{
		Project:  "openclaw",
		Revision: revision,
		Files: map[string]string{
			gatewayDescriptorPath: sha256Hex(gatewayRaw),
		},
	}
	cliSource := sourceMetadata{
		Project:  "openclaw",
		Revision: revision,
		Files: map[string]string{
			coreCLIDescriptorPath: sha256Hex(coreCLIRaw),
			subCLIDescriptorPath:  sha256Hex(subCLIRaw),
		},
	}

	var triage triageConfig
	if err := readJSON(filepath.Join(repoRoot, "docs/parity/gateway-triage.json"), &triage); err != nil {
		return nil, err
	}
	var cliClasses cliClassifications
	if err := readJSON(filepath.Join(repoRoot, "docs/parity/cli-classifications.json"), &cliClasses); err != nil {
		return nil, err
	}
	var deviationDoc coreDeviationDoc
	if err := readJSON(filepath.Join(repoRoot, "docs/parity/core-deviations.json"), &deviationDoc); err != nil {
		return nil, err
	}

	gateway, implemented, err := buildGatewaySnapshot(gatewayDescriptors, gatewaySource, capturedAt, triage)
	if err != nil {
		return nil, err
	}
	cli, err := buildCLISnapshot(coreCLI, subCLI, cliSource, capturedAt, cliClasses)
	if err != nil {
		return nil, err
	}

	supported := methods.SupportedMethods()
	var additional []string
	for _, method := range supported {
		if _, ok := implemented[method]; !ok {
			additional = append(additional, method)
		}
	}
	sort.Strings(additional)
	coreFixture := coreDeviationFixture{
		Source:                    "docs/parity/gateway-method-parity.json",
		CapturedAt:                capturedAt,
		AcceptedMissingMethods:    []string{},
		AcceptedAdditionalMethods: additional,
		AcceptedDeviations:        deviationDoc.AcceptedDeviations,
	}

	gatewayJSON, err := marshalJSON(gateway)
	if err != nil {
		return nil, err
	}
	cliJSON, err := marshalJSON(cli)
	if err != nil {
		return nil, err
	}
	coreJSON, err := marshalJSON(coreFixture)
	if err != nil {
		return nil, err
	}
	return []outputFile{
		{Path: filepath.Join(repoRoot, "docs/parity/gateway-method-parity.json"), Data: gatewayJSON},
		{Path: filepath.Join(repoRoot, "docs/parity/cli-parity.json"), Data: cliJSON},
		{Path: filepath.Join(repoRoot, "cmd/metiqd/testdata/parity/core_method_surface_deviations.json"), Data: coreJSON},
	}, nil
}

func parseDescriptorArray(raw []byte, marker string, advertised bool) ([]descriptor, error) {
	text := string(raw)
	markerAt := strings.Index(text, marker)
	if markerAt < 0 {
		return nil, fmt.Errorf("marker %q not found", marker)
	}
	body := text[markerAt:]
	start := arrayAssignmentRE.FindStringIndex(body)
	if start == nil {
		// CLI catalogs wrap the descriptor array in defineCommandDescriptorCatalog(...).
		const catalogStart = "defineCommandDescriptorCatalog(["
		catalogAt := strings.Index(body, catalogStart)
		if catalogAt < 0 {
			return nil, fmt.Errorf("descriptor array start not found after %q", marker)
		}
		body = body[catalogAt+len(catalogStart):]
	} else {
		body = body[start[1]:]
	}
	end := strings.Index(body, "] as const")
	if end < 0 {
		return nil, fmt.Errorf("descriptor array end not found after %q", marker)
	}
	body = body[:end]

	matches := nameRE.FindAllStringSubmatchIndex(body, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no descriptor names found after %q", marker)
	}
	seen := map[string]struct{}{}
	out := make([]descriptor, 0, len(matches))
	for i, match := range matches {
		name := body[match[2]:match[3]]
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate descriptor %q", name)
		}
		seen[name] = struct{}{}
		chunkEnd := len(body)
		if i+1 < len(matches) {
			chunkEnd = matches[i+1][0]
		}
		chunk := body[match[0]:chunkEnd]
		out = append(out, descriptor{
			Name:       name,
			Advertised: !advertised || !strings.Contains(chunk, "advertise: false"),
		})
	}
	return out, nil
}

func buildGatewaySnapshot(descriptors []descriptor, source sourceMetadata, capturedAt string, triage triageConfig) (gatewaySnapshot, map[string]struct{}, error) {
	validCategories := stringSet(triage.CategoryValues)
	prefixRule := map[string]triageGroup{}
	for _, group := range triage.Groups {
		if group.ID == "" || group.Rationale == "" {
			return gatewaySnapshot{}, nil, errors.New("gateway triage groups require id and rationale")
		}
		if _, ok := validCategories[group.Category]; !ok {
			return gatewaySnapshot{}, nil, fmt.Errorf("unknown gateway triage category %q", group.Category)
		}
		for _, prefix := range group.Prefixes {
			if _, exists := prefixRule[prefix]; exists {
				return gatewaySnapshot{}, nil, fmt.Errorf("gateway triage prefix %q is classified more than once", prefix)
			}
			prefixRule[prefix] = group
		}
	}

	supported := stringSet(methods.SupportedMethods())
	usedPrefixes := map[string]struct{}{}
	implemented := map[string]struct{}{}
	summary := gatewaySummary{
		OpenClawDefinedCount: len(descriptors),
		Partial:              0,
		TriageCategoryCounts: map[string]int{},
	}
	var entries []gatewayEntry
	for _, descriptor := range descriptors {
		if !descriptor.Advertised {
			summary.HiddenMethodCount++
			continue
		}
		summary.OpenClawMethodCount++
		prefix := strings.SplitN(descriptor.Name, ".", 2)[0]
		group, ok := prefixRule[prefix]
		if !ok {
			return gatewaySnapshot{}, nil, fmt.Errorf("advertised gateway method %q has no triage group", descriptor.Name)
		}
		usedPrefixes[prefix] = struct{}{}
		status := "missing"
		metiqMethod := ""
		if _, ok := supported[descriptor.Name]; ok {
			status = "implemented"
			metiqMethod = descriptor.Name
			implemented[descriptor.Name] = struct{}{}
			summary.Implemented++
		} else {
			summary.Missing++
		}
		summary.TriageCategoryCounts[group.Category]++
		entries = append(entries, gatewayEntry{
			Method: descriptor.Name, Status: status, MetiqMethod: metiqMethod,
			Notes: triage.MethodNotes[descriptor.Name], Triage: group.Category,
			TriageGroup: group.ID, Workstream: group.Workstream,
		})
	}
	for prefix := range prefixRule {
		if _, ok := usedPrefixes[prefix]; !ok {
			return gatewaySnapshot{}, nil, fmt.Errorf("stale gateway triage prefix %q matches no advertised method", prefix)
		}
	}
	return gatewaySnapshot{
		SchemaVersion: 2,
		Source:        source,
		CapturedAt:    capturedAt,
		Summary:       summary,
		Entries:       entries,
	}, implemented, nil
}

func buildCLISnapshot(core, sub []descriptor, source sourceMetadata, capturedAt string, cfg cliClassifications) (cliSnapshot, error) {
	validStatuses := stringSet(cfg.StatusValues)
	seen := map[string]struct{}{}
	statusCounts := map[string]int{}
	entries := make([]cliEntry, 0, len(core)+len(sub))
	add := func(descriptors []descriptor, referenceSource string) error {
		for _, descriptor := range descriptors {
			if _, ok := seen[descriptor.Name]; ok {
				return fmt.Errorf("duplicate CLI descriptor %q", descriptor.Name)
			}
			seen[descriptor.Name] = struct{}{}
			classification, ok := cfg.Classifications[descriptor.Name]
			if !ok {
				return fmt.Errorf("CLI descriptor %q has no classification", descriptor.Name)
			}
			if _, ok := validStatuses[classification.Status]; !ok {
				return fmt.Errorf("CLI descriptor %q has invalid status %q", descriptor.Name, classification.Status)
			}
			if classification.Status != "implemented" && classification.Rationale == "" {
				return fmt.Errorf("CLI descriptor %q requires a rationale for status %q", descriptor.Name, classification.Status)
			}
			statusCounts[classification.Status]++
			entries = append(entries, cliEntry{
				Name:            descriptor.Name,
				ReferenceSource: referenceSource,
				Status:          classification.Status,
				MetiqCommand:    classification.MetiqCommand,
				MetiqEntry:      classification.MetiqEntry,
				Rationale:       classification.Rationale,
			})
		}
		return nil
	}
	if err := add(core, "core"); err != nil {
		return cliSnapshot{}, err
	}
	if err := add(sub, "subcli"); err != nil {
		return cliSnapshot{}, err
	}
	for name := range cfg.Classifications {
		if _, ok := seen[name]; !ok {
			return cliSnapshot{}, fmt.Errorf("stale CLI classification %q matches no descriptor", name)
		}
	}
	return cliSnapshot{
		SchemaVersion: 2,
		GeneratedFor:  "metiq CLI parity",
		Reference:     source,
		CapturedAt:    capturedAt,
		StatusValues:  cfg.StatusValues,
		Summary: cliSummary{
			OpenClawCommandCount: len(entries),
			StatusCounts:         statusCounts,
		},
		Groups: entries,
	}, nil
}

func gitMetadata(root string) (revision, capturedAt string, err error) {
	revisionRaw, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", fmt.Errorf("resolve OpenClaw revision: %w", err)
	}
	dateRaw, err := exec.Command("git", "-C", root, "show", "-s", "--format=%cs", "HEAD").Output()
	if err != nil {
		return "", "", fmt.Errorf("resolve OpenClaw revision date: %w", err)
	}
	return strings.TrimSpace(string(revisionRaw)), strings.TrimSpace(string(dateRaw)), nil
}

func readJSON(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func marshalJSON(value any) ([]byte, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func stringSet(items []string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		out[item] = struct{}{}
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "paritygen:", err)
	os.Exit(1)
}

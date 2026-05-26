package qa

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Scenario struct {
	Path             string   `json:"path" yaml:"-"`
	CoverageID       string   `json:"coverage_id" yaml:"coverage_id"`
	Title            string   `json:"title" yaml:"title"`
	Domain           string   `json:"domain" yaml:"domain"`
	RequiredFeatures []string `json:"required_features,omitempty" yaml:"required_features"`
	RequiredPlugins  []string `json:"required_plugins,omitempty" yaml:"required_plugins"`
	ParityTier       string   `json:"parity_tier" yaml:"parity_tier"`
	Lane             string   `json:"lane" yaml:"lane"`
	PSTF             []string `json:"pstf,omitempty" yaml:"pstf"`
	Checks           []Check  `json:"checks,omitempty" yaml:"checks"`
	Body             string   `json:"-" yaml:"-"`
}

type Check struct {
	Type     string `json:"type" yaml:"type"`
	Path     string `json:"path,omitempty" yaml:"path"`
	Pattern  string `json:"pattern,omitempty" yaml:"pattern"`
	MustFind bool   `json:"must_find,omitempty" yaml:"must_find"`
}

type Result struct {
	Path       string `json:"path"`
	CoverageID string `json:"coverage_id"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

type RunReport struct {
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Results    []Result  `json:"results"`
	Passed     int       `json:"passed"`
	Failed     int       `json:"failed"`
}

func LoadFile(path string) (Scenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, err
	}
	sc, err := ParseMarkdown(string(raw))
	if err != nil {
		return Scenario{}, fmt.Errorf("%s: %w", path, err)
	}
	sc.Path = path
	if sc.Domain == "" {
		sc.Domain = filepath.Base(filepath.Dir(path))
	}
	return sc, nil
}

func ParseMarkdown(src string) (Scenario, error) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	if !strings.HasPrefix(src, "---\n") {
		return Scenario{}, errors.New("scenario requires YAML frontmatter")
	}
	rest := src[len("---\n"):]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return Scenario{}, errors.New("frontmatter is not closed")
	}
	var sc Scenario
	if err := yaml.Unmarshal([]byte(rest[:idx]), &sc); err != nil {
		return Scenario{}, err
	}
	sc.Body = strings.TrimSpace(rest[idx+len("\n---"):])
	return sc, Validate(sc)
}

func Validate(sc Scenario) error {
	missing := []string{}
	if sc.CoverageID == "" {
		missing = append(missing, "coverage_id")
	}
	if sc.Title == "" {
		missing = append(missing, "title")
	}
	if sc.ParityTier == "" {
		missing = append(missing, "parity_tier")
	}
	if sc.Lane == "" {
		missing = append(missing, "lane")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}
	if !strings.Contains(sc.Body, "## Steps") || !strings.Contains(sc.Body, "## Expected") {
		return errors.New("scenario body requires ## Steps and ## Expected sections")
	}
	return nil
}

func Discover(root string) ([]Scenario, error) {
	var out []Scenario
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		sc, err := LoadFile(path)
		if err != nil {
			return err
		}
		out = append(out, sc)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

func Run(root, repoRoot string) (RunReport, error) {
	started := time.Now().UTC()
	scenarios, err := Discover(root)
	if err != nil {
		return RunReport{}, err
	}
	report := RunReport{StartedAt: started}
	for _, sc := range scenarios {
		st := time.Now()
		res := runOne(sc, repoRoot)
		res.DurationMS = time.Since(st).Milliseconds()
		report.Results = append(report.Results, res)
		if res.Status == "pass" {
			report.Passed++
		} else {
			report.Failed++
		}
	}
	report.FinishedAt = time.Now().UTC()
	return report, nil
}

func runOne(sc Scenario, repoRoot string) Result {
	res := Result{Path: sc.Path, CoverageID: sc.CoverageID, Status: "pass"}
	for _, check := range sc.Checks {
		target := check.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(repoRoot, target)
		}
		switch check.Type {
		case "file_exists":
			if _, err := os.Stat(target); err != nil {
				res.Status = "fail"
				res.Message = fmt.Sprintf("missing file %s", check.Path)
				return res
			}
		case "grep":
			raw, err := os.ReadFile(target)
			if err != nil {
				res.Status = "fail"
				res.Message = err.Error()
				return res
			}
			re, err := regexp.Compile(check.Pattern)
			if err != nil {
				res.Status = "fail"
				res.Message = err.Error()
				return res
			}
			found := re.Match(raw)
			if found != check.MustFind {
				res.Status = "fail"
				res.Message = fmt.Sprintf("grep %q in %s expected %v got %v", check.Pattern, check.Path, check.MustFind, found)
				return res
			}
		case "metadata_only", "manual":
			continue
		default:
			res.Status = "fail"
			res.Message = "unknown check type: " + check.Type
			return res
		}
	}
	return res
}

func (r RunReport) JSON() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }

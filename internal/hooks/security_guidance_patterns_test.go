package hooks

import "testing"

func TestSecurityGuidancePatternsPositive(t *testing.T) {
	cases := []struct{ name, path, content, rule string }{
		{"github actions", ".github/workflows/ci.yml", "name: ci", "github_actions_workflow"},
		{"exec", "src/a.ts", "exec(`git ${branch}`)", "child_process_exec"},
		{"eval", "src/a.py", "eval(user)", "eval_injection"},
		{"pickle", "src/a.py", "pickle.load(f)", "pickle_deserialization"},
		{"yaml", "src/a.py", "yaml.load(data)", "unsafe_yaml_load"},
		{"torch", "src/a.py", "torch.load(path)", "torch_unsafe_load"},
		{"html", "src/a.jsx", "<div dangerouslySetInnerHTML={{__html: user}} />", "react_dangerously_set_html"},
		{"go shell", "main.go", "exec.Command(\"sh\", \"-c\", cmd)", "go_exec_shell_injection"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !hasRule(CheckSecurityGuidancePatterns(tc.path, tc.content), tc.rule) {
				t.Fatalf("missing rule %s", tc.rule)
			}
		})
	}
}

func TestSecurityGuidancePatternsNegative(t *testing.T) {
	cases := []struct{ name, path, content, rule string }{
		{"exec doc ignored", "README.md", "exec(foo)", "child_process_exec"},
		{"model eval ignored", "src/a.py", "model.eval()", "eval_injection"},
		{"pickle dump ignored", "src/a.py", "pickle.dump(obj, f)", "pickle_deserialization"},
		{"safe yaml", "src/a.py", "yaml.safe_load(data)", "unsafe_yaml_load"},
		{"torch weights", "src/a.py", "torch.load(path, weights_only=True)", "torch_unsafe_load"},
		{"script sri", "index.html", `<script src="https://cdn.example/a.js" integrity="sha384-x"></script>`, "script_src_without_sri"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if hasRule(CheckSecurityGuidancePatterns(tc.path, tc.content), tc.rule) {
				t.Fatalf("unexpected rule %s", tc.rule)
			}
		})
	}
}

func hasRule(findings []SecurityFinding, rule string) bool {
	for _, f := range findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

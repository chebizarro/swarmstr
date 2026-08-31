package commandanalysis

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// FileIdentity pins a concrete executable or interpreter file operand.
type FileIdentity struct {
	CanonicalPath string `json:"canonical_path"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
	Mode          uint32 `json:"mode"`
	ModTimeNS     int64  `json:"mod_time_ns"`
}

// ScriptOperandIdentity pins a local script consumed by an interpreter.
type ScriptOperandIdentity struct {
	ArgvIndex int          `json:"argv_index"`
	Operand   string       `json:"operand"`
	File      FileIdentity `json:"file"`
}

// ExecutionBinding is the approval authority. It contains exact argv and only
// a digest of the sanitized effective environment; environment values are
// never persisted or emitted.
type ExecutionBinding struct {
	Version            int                    `json:"version"`
	CanonicalCWD       string                 `json:"canonical_cwd"`
	Argv               []string               `json:"argv"`
	SanitizedEnvSHA256 string                 `json:"sanitized_env_sha256"`
	Executable         FileIdentity           `json:"executable"`
	CommandExecutable  *FileIdentity          `json:"command_executable,omitempty"`
	ScriptOperand      *ScriptOperandIdentity `json:"script_operand,omitempty"`
	StableSignature    string                 `json:"stable_signature"`
	Fingerprint        string                 `json:"fingerprint"`
}

// ExecutionRequest describes what a tool will actually execute. CommandText is
// used only when the execution is /bin/sh -c <text>; explicit Argv always wins.
type ExecutionRequest struct {
	CommandText string
	Argv        []string
	CWD         string
	Env         map[string]string
}

// CaptureExecutionBinding resolves and hashes the execution context that an
// approval authorizes.
func CaptureExecutionBinding(req ExecutionRequest) (ExecutionBinding, error) {
	cwd, err := canonicalDirectory(req.CWD)
	if err != nil {
		return ExecutionBinding{}, err
	}
	argv := append([]string(nil), req.Argv...)
	if len(argv) == 0 && strings.TrimSpace(req.CommandText) != "" {
		argv = []string{"/bin/sh", "-c", req.CommandText}
	}
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return ExecutionBinding{}, fmt.Errorf("exact execution argv is required")
	}
	for i, arg := range argv {
		if strings.IndexByte(arg, 0) >= 0 {
			return ExecutionBinding{}, fmt.Errorf("argv[%d] contains NUL", i)
		}
	}

	env, err := sanitizedEffectiveEnvironment(req.Env)
	if err != nil {
		return ExecutionBinding{}, err
	}
	executablePath, err := resolveExecutable(argv[0], cwd, env)
	if err != nil {
		return ExecutionBinding{}, err
	}
	executable, err := captureFileIdentity(executablePath, true)
	if err != nil {
		return ExecutionBinding{}, fmt.Errorf("pin executable: %w", err)
	}

	logicalArgv := signatureArgv(req.CommandText, req.Argv)
	binding := ExecutionBinding{
		Version:            1,
		CanonicalCWD:       cwd,
		Argv:               argv,
		SanitizedEnvSHA256: digestEnvironment(env),
		Executable:         executable,
		StableSignature:    StableSignature(Analyze(req.CommandText, logicalArgv)),
	}
	if len(req.Argv) == 0 && len(logicalArgv) > 0 && !isShellBuiltin(logicalArgv[0]) {
		commandPath, err := resolveExecutable(logicalArgv[0], cwd, env)
		if err != nil {
			return ExecutionBinding{}, fmt.Errorf("resolve shell command executable: %w", err)
		}
		if commandPath != executable.CanonicalPath {
			identity, err := captureFileIdentity(commandPath, true)
			if err != nil {
				return ExecutionBinding{}, fmt.Errorf("pin shell command executable: %w", err)
			}
			binding.CommandExecutable = &identity
		}
	}
	if operand, err := captureScriptOperand(req.CommandText, argv, cwd); err != nil {
		return ExecutionBinding{}, err
	} else {
		binding.ScriptOperand = operand
	}
	binding.Fingerprint = fingerprintExecutionBinding(binding)
	return binding, nil
}

// RevalidateExecutionBinding captures the current context again and refuses
// any drift immediately before the execution edge.
func RevalidateExecutionBinding(expected ExecutionBinding, req ExecutionRequest) error {
	actual, err := CaptureExecutionBinding(req)
	if err != nil {
		return fmt.Errorf("recapture execution context: %w", err)
	}
	if expected.Version != 1 || expected.Fingerprint == "" {
		return fmt.Errorf("approval has an invalid execution binding")
	}
	if expected.Fingerprint != actual.Fingerprint || !reflect.DeepEqual(withoutFingerprint(expected), withoutFingerprint(actual)) {
		return fmt.Errorf("approved execution context changed (approved %s, current %s)", expected.Fingerprint, actual.Fingerprint)
	}
	return nil
}

// CloneExecutionBinding returns a deep copy suitable for durable records.
func CloneExecutionBinding(in *ExecutionBinding) *ExecutionBinding {
	if in == nil {
		return nil
	}
	out := *in
	out.Argv = append([]string(nil), in.Argv...)
	if in.CommandExecutable != nil {
		executable := *in.CommandExecutable
		out.CommandExecutable = &executable
	}
	if in.ScriptOperand != nil {
		operand := *in.ScriptOperand
		out.ScriptOperand = &operand
	}
	return &out
}

func canonicalDirectory(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		var err error
		raw, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(abs))
	if err != nil {
		return "", fmt.Errorf("canonicalize cwd: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("stat cwd: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd is not a directory")
	}
	return real, nil
}

func sanitizedEffectiveEnvironment(overrides map[string]string) (map[string]string, error) {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		env[key] = value
	}
	for key, value := range overrides {
		if key == "" || strings.Contains(key, "=") || strings.IndexByte(key, 0) >= 0 || strings.IndexByte(value, 0) >= 0 {
			return nil, fmt.Errorf("invalid environment override key %q", key)
		}
		env[key] = value
	}
	// Values are never persisted; the complete normalized environment is bound
	// through one aggregate digest below. Include secret-valued entries in that
	// digest so changing a credential cannot reuse an approval, while still
	// keeping the credential itself out of the ledger and UI.
	return env, nil
}

func digestEnvironment(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	var size [8]byte
	for _, key := range keys {
		value := env[key]
		binary.BigEndian.PutUint64(size[:], uint64(len(key)))
		_, _ = h.Write(size[:])
		_, _ = io.WriteString(h, key)
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = h.Write(size[:])
		_, _ = io.WriteString(h, value)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func resolveExecutable(name, cwd string, env map[string]string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		candidate := name
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(cwd, candidate)
		}
		return canonicalFilePath(candidate)
	}
	pathValue := env["PATH"]
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}
	for _, dir := range filepath.SplitList(pathValue) {
		if dir == "" {
			dir = cwd
		} else if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
			continue
		}
		return canonicalFilePath(candidate)
	}
	return "", fmt.Errorf("executable %q not found in effective PATH", name)
}

func canonicalFilePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(abs))
	if err != nil {
		return "", err
	}
	return real, nil
}

func captureFileIdentity(path string, requireExecutable bool) (FileIdentity, error) {
	canonical, err := canonicalFilePath(path)
	if err != nil {
		return FileIdentity{}, err
	}
	before, err := os.Stat(canonical)
	if err != nil {
		return FileIdentity{}, err
	}
	if !before.Mode().IsRegular() {
		return FileIdentity{}, fmt.Errorf("%s is not a regular file", canonical)
	}
	if requireExecutable && before.Mode()&0111 == 0 {
		return FileIdentity{}, fmt.Errorf("%s is not executable", canonical)
	}
	file, err := os.Open(canonical)
	if err != nil {
		return FileIdentity{}, err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, file)
	closeErr := file.Close()
	if copyErr != nil {
		return FileIdentity{}, copyErr
	}
	if closeErr != nil {
		return FileIdentity{}, closeErr
	}
	after, err := os.Stat(canonical)
	if err != nil {
		return FileIdentity{}, err
	}
	if before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() {
		return FileIdentity{}, fmt.Errorf("file changed while hashing: %s", canonical)
	}
	return FileIdentity{CanonicalPath: canonical, SHA256: "sha256:" + hex.EncodeToString(h.Sum(nil)), Size: after.Size(), Mode: uint32(after.Mode()), ModTimeNS: after.ModTime().UnixNano()}, nil
}

func captureScriptOperand(commandText string, executionArgv []string, cwd string) (*ScriptOperandIdentity, error) {
	argv := executionArgv
	if len(executionArgv) >= 3 && isShellName(filepath.Base(executionArgv[0])) && executionArgv[1] == "-c" {
		analysis := Analyze(commandText, nil)
		if len(analysis.Segments) != 1 {
			return nil, nil
		}
		argv = analysis.Segments[0].Argv
	}
	index, ok, err := interpreterScriptIndex(argv)
	if err != nil || !ok {
		return nil, err
	}
	operand := argv[index]
	path := operand
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	identity, err := captureFileIdentity(path, false)
	if err != nil {
		return nil, fmt.Errorf("pin interpreter file operand %q: %w", operand, err)
	}
	return &ScriptOperandIdentity{ArgvIndex: index, Operand: operand, File: identity}, nil
}

func interpreterScriptIndex(argv []string) (int, bool, error) {
	if len(argv) < 2 {
		return 0, false, nil
	}
	bin := strings.ToLower(filepath.Base(argv[0]))
	kind := ""
	switch {
	case isShellName(bin):
		kind = "shell"
	case strings.HasPrefix(bin, "python"):
		kind = "python"
	case bin == "node" || bin == "nodejs":
		kind = "node"
	case bin == "ruby":
		kind = "ruby"
	case bin == "perl":
		kind = "perl"
	default:
		return 0, false, nil
	}
	consumeNext := map[string]bool{"-W": true, "-X": true, "-r": true, "--require": true, "--loader": true, "-I": true, "-M": true, "-m": true}
	inline := map[string]bool{"-c": true, "-e": true, "--eval": true, "-p": true, "--print": true}
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			if i+1 < len(argv) {
				return i + 1, true, nil
			}
			return 0, false, nil
		}
		if inline[arg] || (kind == "python" && arg == "-m") {
			return 0, false, nil
		}
		if consumeNext[arg] {
			if i+1 >= len(argv) {
				return 0, false, fmt.Errorf("interpreter flag %s requires a value", arg)
			}
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return i, true, nil
	}
	return 0, false, nil
}

func isShellName(name string) bool {
	switch strings.ToLower(filepath.Base(name)) {
	case "sh", "bash", "dash", "zsh":
		return true
	}
	return false
}

func isShellBuiltin(name string) bool {
	switch strings.ToLower(filepath.Base(name)) {
	case ".", ":", "alias", "bg", "break", "cd", "command", "continue", "echo", "eval", "exec", "exit", "export", "false", "fc", "fg", "getopts", "hash", "jobs", "kill", "printf", "pwd", "read", "readonly", "return", "set", "shift", "test", "times", "trap", "true", "type", "ulimit", "umask", "unalias", "unset", "wait":
		return true
	default:
		return false
	}
}

func signatureArgv(commandText string, argv []string) []string {
	if len(argv) > 0 {
		return append([]string(nil), argv...)
	}
	if strings.TrimSpace(commandText) == "" {
		return nil
	}
	analysis := Analyze(commandText, nil)
	if len(analysis.Segments) == 1 {
		return append([]string(nil), analysis.Segments[0].Argv...)
	}
	return nil
}

func fingerprintExecutionBinding(binding ExecutionBinding) string {
	binding.Fingerprint = ""
	data, _ := json.Marshal(binding)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func withoutFingerprint(binding ExecutionBinding) ExecutionBinding {
	binding.Fingerprint = ""
	return binding
}

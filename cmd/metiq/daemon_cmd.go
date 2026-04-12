package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ─── daemon ───────────────────────────────────────────────────────────────────

// defaultPIDFile returns ~/.metiq/metiqd.pid.
func defaultPIDFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".metiq", "metiqd.pid")
}

// defaultDaemonLog returns ~/.metiq/metiqd.log.
func defaultDaemonLog() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".metiq", "metiqd.log")
}

// resolveDaemonBin returns the path to the metiqd binary.
func resolveDaemonBin(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	self, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(self), "metiqd")
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return exec.LookPath("metiqd")
}

// readPID reads and parses the PID from a pid file.  Returns 0 and no error if
// the file does not exist.
func readPID(pidFile string) (int, error) {
	raw, err := os.ReadFile(pidFile)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("invalid pid file %s: %w", pidFile, err)
	}
	return pid, nil
}

// pidAlive returns true if the process with pid is running and reachable.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; we need to send signal 0.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// processCommandLine returns the process command line for pid via `ps`.
func processCommandLine(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func looksLikeMetiqdCommand(cmdline string) bool {
	cmdline = strings.TrimSpace(cmdline)
	if cmdline == "" {
		return false
	}
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return false
	}
	procPath := strings.ReplaceAll(fields[0], "\\", "/")
	exe := strings.ToLower(filepath.Base(procPath))
	return exe == "metiqd" || exe == "metiqd.exe"
}

// processLooksLikeMetiqd performs strict identity validation for daemon PID
// files to avoid signaling unrelated recycled PIDs.
func processLooksLikeMetiqd(pid int) (bool, string, error) {
	cmdline, err := processCommandLine(pid)
	if err != nil {
		return false, "", err
	}
	if cmdline == "" {
		return false, "", nil
	}
	if looksLikeMetiqdCommand(cmdline) {
		return true, cmdline, nil
	}
	return false, cmdline, nil
}

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var pidFile, logFile, bin, bootstrapPath, adminAddr, adminToken string
	fs.StringVar(&pidFile, "pid-file", "", "PID file path (default: ~/.metiq/metiqd.pid)")
	fs.StringVar(&logFile, "log-file", "", "log file path for start (default: ~/.metiq/metiqd.log)")
	fs.StringVar(&bin, "bin", "", "path to metiqd binary (default: sibling or PATH)")
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path forwarded to metiqd")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (for status check)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}

	sub := fs.Args()
	if len(sub) == 0 {
		fmt.Fprintf(os.Stderr, "daemon subcommands: start, stop, restart, status\n")
		return fmt.Errorf("subcommand required")
	}

	if pidFile == "" {
		pidFile = defaultPIDFile()
	}
	if logFile == "" {
		logFile = defaultDaemonLog()
	}

	switch sub[0] {
	case "start":
		return daemonStart(bin, pidFile, logFile, bootstrapPath, sub[1:])
	case "stop":
		return daemonStop(pidFile)
	case "restart":
		_ = daemonStop(pidFile) // ignore error: may already be down
		time.Sleep(500 * time.Millisecond)
		return daemonStart(bin, pidFile, logFile, bootstrapPath, sub[1:])
	case "status":
		return daemonStatus(pidFile, adminAddr, adminToken, bootstrapPath)
	default:
		return fmt.Errorf("unknown daemon subcommand %q; use start|stop|restart|status", sub[0])
	}
}

func daemonStart(bin, pidFile, logFile, bootstrapPath string, extraArgs []string) error {
	// Check if already running.
	pid, err := readPID(pidFile)
	if err != nil {
		return err
	}
	if pid > 0 && pidAlive(pid) {
		isDaemon, cmdline, idErr := processLooksLikeMetiqd(pid)
		if idErr != nil {
			return fmt.Errorf("daemon pid %d is alive but identity check failed: %w", pid, idErr)
		}
		if isDaemon {
			return fmt.Errorf("daemon already running (pid=%d, pid-file=%s)", pid, pidFile)
		}
		return fmt.Errorf("pid file %s points to non-metiqd process pid=%d (%q); remove stale pid file manually", pidFile, pid, cmdline)
	}

	metiqd, err := resolveDaemonBin(bin)
	if err != nil {
		return fmt.Errorf("cannot find metiqd binary: %w\nSet --bin or ensure metiqd is on PATH", err)
	}

	// Ensure log dir exists.
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", logFile, err)
	}
	defer lf.Close()

	// Build args for metiqd.
	cmdArgs := []string{"--pid-file", pidFile}
	if bootstrapPath != "" {
		cmdArgs = append(cmdArgs, "--bootstrap", bootstrapPath)
	}
	cmdArgs = append(cmdArgs, extraArgs...)

	cmd := exec.Command(metiqd, cmdArgs...)
	cmd.Stdout = lf
	cmd.Stderr = lf
	// Detach from this process group so the child survives our exit.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start metiqd: %w", err)
	}

	fmt.Printf("daemon started  pid=%d  log=%s\n", cmd.Process.Pid, logFile)
	return nil
}

func daemonStop(pidFile string) error {
	pid, err := readPID(pidFile)
	if err != nil {
		return err
	}
	if pid == 0 {
		return fmt.Errorf("no pid file found at %s — daemon may not be running", pidFile)
	}
	if !pidAlive(pid) {
		fmt.Printf("daemon not running (stale pid=%d); removing pid file\n", pid)
		_ = os.Remove(pidFile)
		return nil
	}
	isDaemon, cmdline, err := processLooksLikeMetiqd(pid)
	if err != nil {
		return fmt.Errorf("cannot validate process identity for pid %d: %w", pid, err)
	}
	if !isDaemon {
		return fmt.Errorf("refusing to signal pid %d from %s: process is not metiqd (%q)", pid, pidFile, cmdline)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to pid %d: %w", pid, err)
	}
	// Wait up to 10 s for the process to exit.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		if !pidAlive(pid) {
			fmt.Printf("daemon stopped  pid=%d\n", pid)
			return nil
		}
	}
	// Force kill if still alive.
	_ = proc.Signal(syscall.SIGKILL)
	fmt.Printf("daemon killed   pid=%d (did not stop within 10s)\n", pid)
	return nil
}

func daemonStatus(pidFile, adminAddr, adminToken, bootstrapPath string) error {
	pid, err := readPID(pidFile)
	if err != nil {
		return err
	}

	if pid == 0 {
		fmt.Printf("● metiqd  status=stopped  (no pid file at %s)\n", pidFile)
		return nil
	}
	if !pidAlive(pid) {
		fmt.Printf("● metiqd  status=stopped  (stale pid=%d, pid-file=%s)\n", pid, pidFile)
		return nil
	}
	isDaemon, cmdline, idErr := processLooksLikeMetiqd(pid)
	if idErr != nil {
		fmt.Printf("● metiqd  status=unknown  pid=%d  (identity check failed: %v)\n", pid, idErr)
		return nil
	}
	if !isDaemon {
		fmt.Printf("● metiqd  status=unknown  pid=%d  (pid file points to non-metiqd process: %q)\n", pid, cmdline)
		return nil
	}
	fmt.Printf("● metiqd  status=running  pid=%d\n", pid)

	// Optionally query the admin API for richer info.
	if adminAddr != "" || bootstrapPath != "" {
		cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
		if err != nil {
			fmt.Printf("  (could not reach admin API: %v)\n", err)
			return nil
		}
		result, err := cl.get("/status")
		if err != nil {
			fmt.Printf("  (admin API unreachable: %v)\n", err)
			return nil
		}
		uptime := floatField(result, "uptime_seconds")
		ver := stringField(result, "version")
		pubkey := stringField(result, "pubkey")
		if len(pubkey) > 16 {
			pubkey = pubkey[:16] + "..."
		}
		fmt.Printf("  version=%s  uptime=%.0fs  pubkey=%s\n", ver, uptime, pubkey)
	}
	return nil
}

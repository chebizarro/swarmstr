package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/skip2/go-qrcode"
)

// ─── qr ───────────────────────────────────────────────────────────────────────

func runQR(args []string) error {
	fs := flag.NewFlagSet("qr", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.get("/status")
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}

	pubkey := stringFieldAny(result, "pubkey")
	if pubkey == "" {
		return fmt.Errorf("could not retrieve agent pubkey from daemon")
	}

	// Print nostr: URI and a minimal block-char QR representation.
	nostrURI := "nostr:" + pubkey
	printBlankLine()
	printField("Agent pubkey", pubkey)
	printBlankLine()
	printField("Nostr URI", nostrURI)
	printBlankLine()
	printMuted("(Install a QR-capable terminal or scan the URI with a Nostr client)")
	printBlankLine()
	printTerminalQR(nostrURI)
	return nil
}

// printTerminalQR renders a QR code to the terminal using Unicode half-block
// characters (▀▄█ ) for a compact, scannable representation.
func printTerminalQR(data string) {
	qr, err := qrcode.New(data, qrcode.Medium)
	if err != nil {
		// Fall back to plain text if QR encoding fails.
		fmt.Printf("(QR encode failed: %v)\n", err)
		fmt.Printf("URI: %s\n", data)
		return
	}
	bitmap := qr.Bitmap()
	rows := len(bitmap)
	cols := 0
	if rows > 0 {
		cols = len(bitmap[0])
	}
	// Use pairs of rows to combine into Unicode half-block characters.
	// ▀ = top set, ▄ = bottom set, █ = both set, " " = neither.
	for y := 0; y < rows; y += 2 {
		for x := 0; x < cols; x++ {
			top := bitmap[y][x]
			bottom := false
			if y+1 < rows {
				bottom = bitmap[y+1][x]
			}
			switch {
			case top && bottom:
				fmt.Print("█")
			case top && !bottom:
				fmt.Print("▀")
			case !top && bottom:
				fmt.Print("▄")
			default:
				fmt.Print(" ")
			}
		}
		fmt.Println()
	}
}

// ─── completion ───────────────────────────────────────────────────────────────

func runCompletion(args []string) error {
	shell := "bash"
	if len(args) > 0 {
		shell = args[0]
	}
	switch shell {
	case "bash":
		fmt.Print(bashCompletion())
	case "zsh":
		fmt.Print(zshCompletion())
	case "fish":
		fmt.Print(fishCompletion())
	default:
		return fmt.Errorf("unknown shell %q; supported: bash, zsh, fish", shell)
	}
	return nil
}

func bashCompletion() string {
	commands := strings.Join(currentRegistry().commandNames(), " ")
	return fmt.Sprintf(`# metiq bash completion
# Add to ~/.bashrc:  source <(metiq completion bash)
_metiq_completions() {
	local commands=%q
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=($(compgen -W "${commands}" -- "${cur}"))
}
complete -F _metiq_completions metiq
`, commands)
}

func zshCompletion() string {
	var b strings.Builder
	b.WriteString("# metiq zsh completion\n")
	b.WriteString("# Add to ~/.zshrc:  source <(metiq completion zsh)\n")
	b.WriteString("_metiq() {\n  local commands=(\n")
	for _, cmd := range currentRegistry().visibleCommands() {
		fmt.Fprintf(&b, "    '%s:%s'\n", cmd.Name, cmd.Summary)
	}
	b.WriteString("  )\n  _describe 'commands' commands\n}\ncompdef _metiq metiq\n")
	return b.String()
}

func fishCompletion() string {
	var b strings.Builder
	b.WriteString("# metiq fish completion\n")
	b.WriteString("# Add to ~/.config/fish/completions/metiq.fish or: metiq completion fish | source\n")
	for _, cmd := range currentRegistry().commandNames() {
		fmt.Fprintf(&b, "complete -c metiq -f -n '__fish_use_subcommand' -a %s\n", cmd)
	}
	return b.String()
}

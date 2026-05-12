package main

import (
	"flag"
	"fmt"

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
		fmt.Print(bashCompletion)
	case "zsh":
		fmt.Print(zshCompletion)
	case "fish":
		fmt.Print(fishCompletion)
	default:
		return fmt.Errorf("unknown shell %q; supported: bash, zsh, fish", shell)
	}
	return nil
}

const bashCompletion = `# metiq bash completion
# Add to ~/.bashrc:  source <(metiq completion bash)
_metiq_completions() {
	local commands="version status health logs observe models channels agents skills hooks secrets update security plugins config nodes sessions cron approvals doctor qr completion daemon gw plan bootstrap-check dm-send memory-search"
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=($(compgen -W "${commands}" -- "${cur}"))
}
complete -F _metiq_completions metiq
`

const zshCompletion = `# metiq zsh completion
# Add to ~/.zshrc:  source <(metiq completion zsh)
_metiq() {
  local commands=(
    'version:show version'
    'status:show daemon status'
    'health:health check'
    'logs:stream logs'
    'observe:structured runtime observability'
    'models:model management'
    'channels:channel management'
    'agents:agent management'
    'skills:skill management'
    'hooks:hook management'
    'secrets:secret management'
    'update:update metiq'
    'security:security audit'
    'plugins:plugin management'
    'config:config management'
    'nodes:remote node management'
    'sessions:session management'
    'cron:scheduled task management'
    'approvals:exec approval management'
    'doctor:system health diagnostics'
    'qr:display agent QR code'
    'completion:generate shell completions'
	'daemon:daemon lifecycle management'
	'gw:gateway method passthrough'
  )
  _describe 'commands' commands
}
compdef _metiq metiq
`

const fishCompletion = `# metiq fish completion
# Add to ~/.config/fish/completions/metiq.fish or: metiq completion fish | source
for cmd in version status health logs observe models channels agents skills hooks secrets update security plugins config nodes sessions cron approvals doctor qr completion daemon gw
  complete -c metiq -f -n '__fish_use_subcommand' -a $cmd
end
`

// Command build-webui assembles internal/webui/ui.html from the ordered
// fragments under internal/webui/src/.  It is pure Go (no Node toolchain) so
// it can run anywhere the repo builds, including CI:
//
//	go generate ./internal/webui
//	go run metiq/scripts/build-webui
//
// The generated ui.html is committed; TestUIHTMLIsAssembledFromSources in
// internal/webui fails when the artifact drifts from its sources.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"metiq/internal/webui"
)

func main() {
	srcDir := flag.String("src", "src", "fragment source directory (relative to -dir)")
	out := flag.String("out", "ui.html", "output file (relative to -dir)")
	dir := flag.String("dir", "", "webui package directory (defaults to ./internal/webui if it exists, else cwd)")
	flag.Parse()

	base := *dir
	if base == "" {
		if _, err := os.Stat(filepath.Join("internal", "webui", "src")); err == nil {
			base = filepath.Join("internal", "webui")
		} else {
			base = "."
		}
	}

	html, err := webui.AssembleUI(filepath.Join(base, *srcDir))
	if err != nil {
		fmt.Fprintln(os.Stderr, "build-webui:", err)
		os.Exit(1)
	}
	target := filepath.Join(base, *out)
	if err := os.WriteFile(target, html, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "build-webui:", err)
		os.Exit(1)
	}
	fmt.Printf("build-webui: wrote %s (%d bytes from %d fragments)\n", target, len(html), len(webui.UISourceFiles))
}

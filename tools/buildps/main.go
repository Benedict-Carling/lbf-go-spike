// Command buildps writes lbf.ps1: go run ./tools/buildps -version v0.2.0 -o dist/lbf.ps1
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Benedict-Carling/lbf-cli/internal/psbuild"
)

func main() {
	version := flag.String("version", "dev", "version lbf.ps1 reports")
	out := flag.String("o", "dist/lbf.ps1", "output file")
	flag.Parse()
	script, err := psbuild.Build(".", *version)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(*out), 0o755)
	}
	if err == nil {
		err = os.WriteFile(*out, script, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "buildps:", err)
		os.Exit(1)
	}
}

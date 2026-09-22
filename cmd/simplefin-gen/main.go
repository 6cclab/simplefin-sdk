// Command simplefin-gen generates SimpleFIN SDKs from spec/simplefin.yaml.
//
//	simplefin-gen generate --lang go   --out ./sdk/go --package simplefin
//	simplefin-gen generate --lang node --out ./sdk/node
//	simplefin-gen verify    # regenerate and diff against what is committed
//	simplefin-gen langs     # list available targets
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andrepato/simplefin-sdk/internal/gen"
	"github.com/andrepato/simplefin-sdk/internal/ir"
)

// defaultOut is where each language is generated when --out is omitted, and
// what `verify` checks against.
var defaultOut = map[string]string{
	"go":   filepath.Join("sdk", "go"),
	"node": filepath.Join("sdk", "node"),
}

// defaultPackage is the Go package name for targets that need one.
const defaultPackage = "simplefin"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "simplefin-gen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("a command is required")
	}

	switch args[0] {
	case "generate":
		return cmdGenerate(args[1:])
	case "verify":
		return cmdVerify(args[1:])
	case "langs":
		return cmdLangs()
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `simplefin-gen — generate SimpleFIN SDKs from the protocol spec

Commands:
  generate   Render one language target
  verify     Regenerate every target and diff against what is committed
  langs      List available language targets

Run "simplefin-gen <command> -h" for command flags.
`)
}

func cmdGenerate(args []string) error {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	var (
		lang    = fs.String("lang", "", "target language (required; see `simplefin-gen langs`)")
		out     = fs.String("out", "", "output directory (defaults per language)")
		pkg     = fs.String("package", defaultPackage, "package name for targets that need one")
		specArg = fs.String("spec", defaultSpecPath(), "path to simplefin.yaml")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *lang == "" {
		return fmt.Errorf("--lang is required (see `simplefin-gen langs`)")
	}

	spec, err := ir.Load(*specArg)
	if err != nil {
		return err
	}

	files, err := gen.Generate(spec, *lang, *pkg)
	if err != nil {
		return err
	}

	dir := *out
	if dir == "" {
		d, ok := defaultOut[*lang]
		if !ok {
			return fmt.Errorf("no default output directory for %q; pass --out", *lang)
		}
		dir = filepath.Join(repoRoot(*specArg), d)
	}

	for _, f := range files {
		target := filepath.Join(dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, f.Content, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", target, err)
		}
		fmt.Println("wrote", target)
	}
	return nil
}

// cmdVerify regenerates every target in memory and compares it to what is on
// disk. This is the CI gate that makes hand-editing a generated file fail the
// build rather than silently survive until the next regeneration erases it.
func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	var (
		pkg     = fs.String("package", defaultPackage, "package name for targets that need one")
		specArg = fs.String("spec", defaultSpecPath(), "path to simplefin.yaml")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	spec, err := ir.Load(*specArg)
	if err != nil {
		return err
	}
	langs, err := gen.Languages()
	if err != nil {
		return err
	}

	root := repoRoot(*specArg)
	var stale []string

	for _, lang := range langs {
		files, err := gen.Generate(spec, lang, *pkg)
		if err != nil {
			return err
		}
		dir, ok := defaultOut[lang]
		if !ok {
			return fmt.Errorf("no default output directory for %q", lang)
		}
		for _, f := range files {
			target := filepath.Join(root, dir, f.Path)
			onDisk, err := os.ReadFile(target)
			if err != nil {
				stale = append(stale, fmt.Sprintf("%s (missing)", target))
				continue
			}
			if !bytes.Equal(onDisk, f.Content) {
				stale = append(stale, fmt.Sprintf("%s (differs)", target))
			}
		}
	}

	if len(stale) > 0 {
		sort.Strings(stale)
		return fmt.Errorf("generated files are out of date:\n  - %s\n\nRun `simplefin-gen generate` for each language and commit the result.",
			strings.Join(stale, "\n  - "))
	}

	fmt.Printf("verify: %d language target(s) up to date\n", len(langs))
	return nil
}

func cmdLangs() error {
	langs, err := gen.Languages()
	if err != nil {
		return err
	}
	for _, l := range langs {
		if out, ok := defaultOut[l]; ok {
			fmt.Printf("%-6s -> %s\n", l, out)
			continue
		}
		fmt.Printf("%-6s (no default output directory)\n", l)
	}
	return nil
}

// defaultSpecPath looks for the spec relative to the working directory, so the
// generator works from the repo root without flags.
func defaultSpecPath() string {
	return filepath.Join("spec", "simplefin.yaml")
}

// repoRoot derives the repository root from the spec path, so --out defaults
// resolve correctly even when the generator is run from a subdirectory.
func repoRoot(specPath string) string {
	abs, err := filepath.Abs(specPath)
	if err != nil {
		return "."
	}
	return filepath.Dir(filepath.Dir(abs))
}

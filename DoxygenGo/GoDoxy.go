// GoDoxy is the entry point for the GoDoxy documentation generator.
// It wires all internal packages together and drives the full documentation
// pipeline described in sequence.puml:
//
//  1. Parse and validate CLI flags (cli).
//  2. Scan the source directory for Unreal Engine .h files (scanner).
//  3. Tokenise and parse each header file into structured macros (parser).
//  4. Extract DocumentedType trees from the parsed macros (extractor).
//  5. Build a cross-reference map so the generator can hyperlink types (crossref).
//  6. Render documentation files to the output directory (generator).
//  7. Write the client-side search index (search).
//
// Usage:
//
//	godoxy -source <path> -out <path> [-format md|html]
package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"playground/GoDoxy/internal/cli"
	"playground/GoDoxy/internal/crossref"
	"playground/GoDoxy/internal/extractor"
	"playground/GoDoxy/internal/generator"
	"playground/GoDoxy/internal/parser"
	"playground/GoDoxy/internal/scanner"
	"playground/GoDoxy/internal/search"
	"playground/GoDoxy/internal/types"
)

func main() {
	fmt.Println("📚 GoDoxy is generating documentation...")
	// Phase 0: Parse and validate CLI flags.
	cfg, err := cli.Parse()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		cli.PrintUsage()
		os.Exit(1)
	}

	// Verify / create the output directory before doing any real work.
	if err := os.MkdirAll(cfg.OutDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot create output directory %q: %v\n", cfg.OutDir, err)
		os.Exit(1)
	}

	// Set up a warning logger that writes to both stderr and godoxygo.log.
	logFile, err := os.Create(cfg.OutDir + "/godoxygo.log")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot create log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()
	warn := log.New(io.MultiWriter(os.Stderr, logFile), "WARN: ", 0)

	// Phase 1: Scan for header files.
	headers, err := scanner.Scan(cfg.SourceDir)
	if err != nil || len(headers) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no .h files found at source path:", cfg.SourceDir)
		os.Exit(1)
	}

	// Phase 2: Parse each header file into a flat list of macros.
	var allMacros []types.ParsedMacro
	warningCount := 0

	for _, path := range headers {
		content, err := os.ReadFile(path)
		if err != nil {
			warn.Printf("skipping %s: %v", path, err)
			warningCount++
			continue
		}

		macros, parseErrs := parser.Parse(string(content), path)
		for _, pe := range parseErrs {
			warn.Print(pe.Error())
			warningCount++
		}
		allMacros = append(allMacros, macros...)
	}

	// Phase 3: Extract structured DocumentedType trees from the macro list.
	docs := extractor.Extract(allMacros)

	// Phase 4: Build the cross-reference map (TypeName -> OutputFilePath).
	refs := crossref.Build(docs)

	// Phase 5: Render documentation files to the output directory.
	if err := generator.Generate(docs, refs, cfg.OutDir, cfg.Format); err != nil {
		warn.Printf("generator error: %v", err)
		warningCount++
	}

	// Phase 6: Write the client-side search index.
	if err := search.WriteIndex(docs, cfg.OutDir); err != nil {
		warn.Printf("search index error: %v", err)
		warningCount++
	}

	if warningCount > 0 {
		fmt.Printf("📚 GoDoxy completed with %d warning(s) — see %s/godoxygo.log\n", warningCount, cfg.OutDir)
	} else {
		fmt.Println("📚 GoDoxy documentation successfully generated!")
	}
}

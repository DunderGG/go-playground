// GoPurge scans an Unreal Engine project directory to identify
// duplicate assets, unreferenced files, and large "waste" files.
//
// It helps developers maintain a lean project for faster backups and
// Git LFS management.
//
// Usage:
//
//	gopurge -project-dir=<path> [-output=json|csv] [-workers=N] [-large-threshold=100]
//
// Flags:
//
//	-project-dir, p      Path to the root of the Unreal Engine project (required).
//	-output, o           Report format: "json" (default) or "csv".
//	-workers, w          Number of goroutines used for SHA-256 hashing (default 4).
//	-large-threshold, l  Size in MB above which a file is considered "large" (default 100).
//	-report-path, r      Output path for the report file (default: gopurge_report.<ext>).
//
// GoPurge is read-only — it never modifies or deletes any files.
// Always run it while the Unreal Editor is closed.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
	"GoPurge/preflight"
)

func main() {
	// ── CLI flags ──────────────────────────────────────────────────────────
	var (
		projectDir       string
		outputFormat     string
		workers          int
		largeThresholdMB int64
		reportPath       string
	)
	flag.StringVar(&projectDir, "project-dir", "", "Path to the Unreal Engine project root (required)")
	flag.StringVar(&projectDir, "p", "", "Path to the Unreal Engine project root (required)")
	flag.StringVar(&outputFormat, "output", reporter.FormatJSON, `Report format: "json" or "csv"`)
	flag.StringVar(&outputFormat, "o", reporter.FormatJSON, `Report format: "json" or "csv"`)
	flag.IntVar(&workers, "workers", 4, "Number of goroutines for SHA-256 hashing")
	flag.IntVar(&workers, "w", 4, "Number of goroutines for SHA-256 hashing")
	flag.Int64Var(&largeThresholdMB, "large-threshold", 100, "File size in MB above which a file is flagged as large")
	flag.Int64Var(&largeThresholdMB, "l", 100, "File size in MB above which a file is flagged as large")
	flag.StringVar(&reportPath, "report-path", "", `Output path for the report file (default: gopurge_report.<ext>)`)
	flag.StringVar(&reportPath, "r", "", `Output path for the report file (default: gopurge_report.<ext>)`)
	flag.Parse()

	if projectDir == "" {
		fmt.Fprintln(os.Stderr, "error: -project-dir is required")
		flag.Usage()
		os.Exit(1)
	}

	// Resolve default report path if not specified.
	if reportPath == "" {
		ext := outputFormat
		if ext != reporter.FormatCSV {
			ext = reporter.FormatJSON
		}
		reportPath = filepath.Join(".", "gopurge_report."+ext)
	}

	largeThresholdBytes := largeThresholdMB * 1024 * 1024

	// SetFlags(0) to disable timestamps and other prefixes in log output and
	// take "full control", since all warnings are collected in the report and
	// printed in a summary at the end.
	log.SetFlags(0)
	log.SetPrefix("gopurge: ")

	fmt.Println("🧹 GoPurge is ready to scan...")
	fmt.Printf("   Project:   %s\n", projectDir)
	fmt.Printf("   Output:    %s (%s)\n", reportPath, outputFormat)
	fmt.Printf("   Workers:   %d\n", workers)
	fmt.Printf("   Large ≥:   %d MB\n\n", largeThresholdMB)

	// ── 1. Pre-flight validation ───────────────────────────────────────────
	fmt.Println("→ Running pre-flight checks...")
	if err := preflight.Validate(projectDir); err != nil {
		log.Fatalf("pre-flight failed: %v", err)
	}
	fmt.Println("  ✓ Pre-flight checks passed.")

	// ── 2. Project discovery ───────────────────────────────────────────────
	// ── 3. Duplicate detection ─────────────────────────────────────────────
	// ── 4. Large file detection ────────────────────────────────────────────
	// ── 5. Reference analysis ──────────────────────────────────────────────
	// ── 6. Assemble report ─────────────────────────────────────────────────
	// ── 7. Write report ────────────────────────────────────────────────────
}

}

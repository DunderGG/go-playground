// GoPurge scans an Unreal Engine project directory to identify
// duplicate assets, unreferenced files, and large "waste" files.
//
// It helps developers maintain a lean project for faster backups and
// Git LFS management.
//
// Features to add:
//   - Fast parallel disk walking using Goroutines for hashing file contents.
//   - Search for duplicate assets based on file size and MD5/SHA checksums.
//   - Integration with .uproject files to identify assets not referenced in the graph.
//   - Generation of a CSV or JSON report listing all "safe to delete" candidates.
//
// Common Pitfalls:
//   - File Locking: Unreal Editor often holds locks on .uasset files; handle "access denied" gracefully.
//   - Symbolic Links: Recursive walking can enter infinite loops if symlinks/junctions aren't handled.
//   - Memory Usage: Large project scans can consume massive RAM if file hashes aren't streamed.
//   - Path Lengths: Windows has a MAX_PATH limit (260 chars) that Unreal projects often exceed.
//
// Pitfalls found from the existing Unreal Engine filter "not used in any level/asset":
//   - Certain game data files might be flagged as unused, which could lead to accidental deletion and project issues.
//
// Note: This tool should always be run while the Unreal Editor is closed.
package main

import (
	"fmt"
)

func main() {
	fmt.Println("🧹 GoPurge is ready to scan...")

	/*
		   DETAILED IMPLEMENTATION PLAN
		   ----------------------------

		   1. PROJECT DISCOVERY & OS OPTIMIZATION
		      - Walk "Content/" using `filepath.WalkDir`. Skip "Intermediate/" and "DerivedDataCache/".
		      - Use `os.Lstat` to detect and skip (or carefully follow) symbolic links.
		      - Use `\\?\` prefix on Windows paths if lengths exceed 260 characters.

		   2. MULTI-STAGE DUPLICATE DETECTION
		      - Stage A (Size): Group files by size. Ignore files unique in size.
		      - Stage B (Header): read first 1KB of overlapping files to differentiate quickly.
		      - Stage C (Full Hash): Use a Worker Pool of 4-8 Goroutines.
		      - Stream file content into `sha256.New()` via `io.Copy` to keep RAM usage under 50MB.

		   3. REFERENCE ANALYSIS (THE "HARD" PART)
		      - Unreal assets use "Soft Object Paths" (strings) to reference each other.
		      - Search for strings like `/Game/Path/To/Asset` inside all .uasset files (binary search).
		      - Cross-reference with Source/ code parsing (Regex for `FSoftObjectPath`).

		   4. REPORT GENERATION
		      - Print a summary of "Total Waste" in MB/GB.
		      - Output a `clean_report.json` with categories: "Duplicate", "Unreferenced", "Large".
			  - Never delete! User should always review the report and manually delete after verifying in Unreal Editor.
	*/
}

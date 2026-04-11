// IncludeAudit analyzes Unreal C++ source and header files to identify
// redundant #include statements and suggest forward declarations.
//
// It aims to reduce compilation times by pruning the include graph and
// highlighting unnecessary dependencies in Public headers.
//
// Features to add:
//   - Static analysis of symbol usage to detect unused #include lines.
//   - Recommendation engine for replacing full includes with Forward Declarations.
//   - Detection of circular dependencies between different Unreal modules.
//   - Reporting tool that estimates "Compile Time Debt" based on include depth.
//   - A graphical representation of the include graph to visualize hotspots and bottlenecks.
//
// Common Pitfalls:
//   - False Positives: Macros like `UPROPERTY` might require an include that the parser doesn't "see" used.
//   - Generated Files: Don't audit `.generated.h` files; they are managed by Unreal Header Tool.
//   - Transitive Includes: Removing A might break B if B was relying on A including C.
//   - Third-Party headers: Treat external libs as "black boxes" to avoid incorrect pruning.
//
// Note: Use while the project is in a "clean" state for best analysis results.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FileAudit stores analysis for a single file
type FileAudit struct {
	Path     string
	Includes []string
	Symbols  []string
}

func main() {
	// We determine which folder to scan (defaulting to ".") and start the analysis.
	targetDir := "." // Default to current directory
	if len(os.Args) > 1 {
		// os.Args[1] is the first command-line argument passed to the program.
		targetDir = os.Args[1]
	}

	fmt.Printf("🔍 Scanning: %s\n", targetDir)

	// Regex for C++ includes: Look for #include followed by "name.h" or <name.h>
	// ^#include  : Starts with #include
	// \s+       : One or more spaces
	// ["<]      : Opening quote or bracket
	// ([^">]+)  : Capture group: any character except " or >
	includeRegex := regexp.MustCompile(`^#include\s+["<]([^">]+)[">]`)

	// Regex for Unreal Symbols: Look for 'class', 'struct', or 'enum' followed by a name
	// starting with an uppercase letter (Unreal convention: A, U, F, etc.)
	symbolRegex := regexp.MustCompile(`\b(class|struct|enum)\s+([A-Z][a-zA-Z0-9_]+)`)

	// Walk through the directory tree recursively.
	// filepath.Walk takes a root path and a callback function to run for every file/folder.
	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// Check file extensions (case-insensitive)
		ext := strings.ToLower(filepath.Ext(path))

		// Logic: Only audit C++ files, but skip Unreal's auto-generated headers
		if (ext == ".h" || ext == ".cpp") && !strings.Contains(path, ".generated.h") {
			// 1. Process the file to get its audit data
			audit := scanFile(path, includeRegex, symbolRegex)
			// 2. Output the findings to the terminal
			printAudit(audit)
		}
		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking path: %v\n", err)
	}
}

// scanFile opens a file and reads it line-by-line to find includes and symbols.
// Input:
//   - path: The absolute or relative path to the file.
//   - iRegex: The compiled regex for detecting #include.
//   - sRegex: The compiled regex for detecting Class/Struct symbols.
//
// Output:
//   - A FileAudit struct containing the results of the scan.
func scanFile(path string, iRegex, sRegex *regexp.Regexp) FileAudit {
	file, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening file %s: %v\n", path, err)
		return FileAudit{Path: path}
	}
	defer file.Close() // Ensures the file is closed when the function returns

	audit := FileAudit{Path: path}
	scanner := bufio.NewScanner(file)

	// Process each line of the file (equivalent to std::getline in C++)
	for scanner.Scan() {
		// Trim whitespace from the line to simplify regex matching
		line := strings.TrimSpace(scanner.Text())

		// Extract Include: uses the regex capture group ([^">]+)
		if matches := iRegex.FindStringSubmatch(line); len(matches) > 1 {
			audit.Includes = append(audit.Includes, matches[1])
		}

		// Extract Symbol: uses the second capture group ([A-Z][a-zA-Z0-9_]+)
		if matches := sRegex.FindStringSubmatch(line); len(matches) > 2 {
			audit.Symbols = append(audit.Symbols, matches[2])
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file %s: %v\n", path, err)
	}
	return audit
}

// printAudit displays the results of a file scan in a formatted list.
// Input:
//   - audit: The FileAudit struct containing path, includes, and symbols.
func printAudit(audit FileAudit) {
	// Guess we found no includes or symbols...
	if len(audit.Includes) == 0 && len(audit.Symbols) == 0 {
		return
	}

	fmt.Printf("\n📄 File: %s\n", audit.Path)
	if len(audit.Includes) > 0 {
		fmt.Printf("   ├─ Includes (%d): %s\n", len(audit.Includes), strings.Join(audit.Includes, ", "))
	}
	if len(audit.Symbols) > 0 {
		fmt.Printf("   └─ Symbols  (%d): %s\n", len(audit.Symbols), strings.Join(audit.Symbols, ", "))
	}
}

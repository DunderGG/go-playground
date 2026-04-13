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

var (
	// IncludeRegex matches C++ includes: Look for #include followed by "name.h" or <name.h>
	// ^#include  : Starts with #include
	// \s+       : One or more spaces
	// ["<]      : Opening quote or bracket
	// ([^">]+)  : Capture group: any character except " or >
	IncludeRegex = regexp.MustCompile(`^#include\s+["<]([^">]+)[">]`)

	// SymbolRegex matches Unreal Symbols: Look for 'class', 'struct', 'enum', or 'namespace'
	// followed by an optional API macro (e.g. UTILITYFEATURES_API) and the symbol name.
	// It ensures it doesn't match forward declarations by checking it's NOT followed by a semicolon.
	SymbolRegex = regexp.MustCompile(`\b(class|struct|enum|namespace)\s+(?:[A-Z0-9_]+_API\s+)?([a-zA-Z0-9_]+)\s*[{:]`)
)

// GetFullUsageRegex returns a compiled regex for detecting "Full" usage of a specific symbol.
// Full usage includes member access (->, .), construction (new, constructor call),
// static calls (::), or being used in template arguments (Cast<T>, NewObject<T>).
func GetFullUsageRegex(symbol string, headerText string) *regexp.Regexp {
	// 1. Core usage: symbol.Member, symbol->Member, symbol(), etc.
	pattern := fmt.Sprintf(`\b%s\b\s*(\.|->|::|\(|[a-zA-Z0-9_]+\s*(\[|;|\s+|=|\())|<\s*%s\s*>|new\s+\b%s\b`, symbol, symbol, symbol)

	// 2. Variable usage: Find variables of this type in the header
	// Pattern: TObjectPtr<Symbol> SomeVar; or Symbol* SomeVar;
	varNames := []string{}

	// Look for TObjectPtr<Symbol> Name;
	tPtrRegex := regexp.MustCompile(fmt.Sprintf(`TObjectPtr\s*<\s*(?:class|struct)?\s*\b%s\b\s*>\s+([a-zA-Z0-9_]+)`, symbol))
	for _, m := range tPtrRegex.FindAllStringSubmatch(headerText, -1) {
		if len(m) > 1 {
			varNames = append(varNames, m[1])
		}
	}
	// Look for Symbol* Name;
	ptrRegex := regexp.MustCompile(fmt.Sprintf(`(?:\bclass\b|\bstruct\b)?\s*\b%s\b\s*\*+\s*([a-zA-Z0-9_]+)`, symbol))
	for _, m := range ptrRegex.FindAllStringSubmatch(headerText, -1) {
		if len(m) > 1 {
			varNames = append(varNames, m[1])
		}
	}

	// Add variable member access to pattern: varName-> or varName.
	for _, name := range varNames {
		pattern += fmt.Sprintf(`|\b%s\b\s*(?:->|\.|\()`, name)
	}

	return regexp.MustCompile(pattern)
}

// FileSummary stores the final audit info for a file to be displayed in the HTML dashboard.
type FileSummary struct {
	Path           string
	Includes       []IncludeStatus
	TotalIncludes  int
	RedundantCount int
	ForwardCount   int
}

// IncludeStatus tracks the status of a specific include within a file.
type IncludeStatus struct {
	Name             string
	Status           string // "Essential", "Redundant", "Unknown", or "Forward"
	SuggestedForward string // e.g., "class AMyCharacter;"
}

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
		targetDir = os.Args[1]
	}

	fmt.Printf("🔍 Scanning: %s\n", targetDir)

	// Step 1: Build the Global Symbol Registry (where symbols are defined)
	// Key: Symbol Name (e.g. "APlayerCharacter")
	// Value: Slice of strings representing header files that define it
	symbolRegistry, err := buildSymbolRegistry(targetDir, IncludeRegex, SymbolRegex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building registry: %v\n", err)
		return
	}

	// Print the collected Registry so we can see what we've mapped
	fmt.Printf("\n📚 Global Symbol Registry (%d entries):\n", len(symbolRegistry))
	for sym, headers := range symbolRegistry {
		fmt.Printf("   %-20s -> %s\n", sym, strings.Join(headers, ", "))
	}

	// Step 2: Second Pass - Identify redundant includes in .cpp files
	fmt.Println("\n🔍 Analyzing .cpp files for redundant includes...")
	summaries, err := analyzeCppFiles(targetDir, IncludeRegex, symbolRegistry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error analyzing files: %v\n", err)
	}

	// Step 3: Generate the Dashboard
	fmt.Println("\n📊 Generating dashboard.html...")
	err = generateDashboard(summaries)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating dashboard: %v\n", err)
	}
}

// buildSymbolRegistry performs the "First Pass" by scanning all headers (.h)
// and mapping every symbol found to the filename it belongs to.
//
// Input:
//   - dir: The root folder to scan for .h files.
//   - includeRegex: The compiled regex for detecting #include.
//   - symbolRegex: The compiled regex for detecting Class/Struct symbols.
//
// Output:
//   - A map where the key is the Symbol and the value is a slice of Header filenames.
//   - error: Returns any directory traversal errors.
func buildSymbolRegistry(dir string, includeRegex, symbolRegex *regexp.Regexp) (map[string][]string, error) {
	symbolRegistry := make(map[string][]string)

	err := filepath.WalkDir(dir, func(path string, dirEntry os.DirEntry, err error) error {
		if err != nil || dirEntry.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))

		// Only look at Headers (.h) to find where symbols are DEFINED
		// Skip .generated.h files since they are auto-generated by Unreal Header Tool.
		if ext == ".h" && !strings.Contains(path, ".generated.h") {
			audit := scanFile(path, includeRegex, symbolRegex)

			// Clean the file path/name for storage and matching
			headerName := filepath.Base(path)

			// Register each symbol found in this header
			for _, symbol := range audit.Symbols {
				// Store the symbol and the filename where it lives
				// We check if it is already in the list to avoid exact duplicates
				found := false
				for _, existingHeader := range symbolRegistry[symbol] {
					if existingHeader == headerName {
						found = true
						break
					}
				}
				if !found {
					symbolRegistry[symbol] = append(symbolRegistry[symbol], headerName)
				}
			}
		}
		return nil
	})

	return symbolRegistry, err
}

// scanFile opens a file and reads it line-by-line to find includes and symbols.
// Input:
//   - path: The absolute or relative path to the file.
//   - includeRegex: The compiled regex for detecting #include.
//   - symbolRegex: The compiled regex for detecting Class/Struct symbols.
//
// Output:
//   - A FileAudit struct containing the results of the scan.
func scanFile(path string, includeRegex, symbolRegex *regexp.Regexp) FileAudit {
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
		if matches := includeRegex.FindStringSubmatch(line); len(matches) > 1 {
			audit.Includes = append(audit.Includes, matches[1])
		}

		// Extract Symbol: uses the second capture group ([A-Z][a-zA-Z0-9_]+)
		if matches := symbolRegex.FindStringSubmatch(line); len(matches) > 2 {
			audit.Symbols = append(audit.Symbols, matches[2])
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file %s: %v\n", path, err)
	}
	return audit
}

// analyzeCppFiles performs the "Second Pass" by scanning .cpp files to see
// which of their included headers are actually needed based on symbol usage.
//
// Input:
//   - dir: The root folder to scan for .cpp files.
//   - includeRegex: The compiled regex for detecting #include.
//   - symbolRegistry: Our global map[Symbol][]Headers from the first pass.
//
// Output:
//   - []FileSummary: A list of audit results for each file.
//   - error: Returns any directory traversal errors.
func analyzeCppFiles(dir string, includeRegex *regexp.Regexp, symbolRegistry map[string][]string) ([]FileSummary, error) {
	var summaries []FileSummary

	err := filepath.WalkDir(dir, func(path string, dirEntry os.DirEntry, err error) error {
		if err != nil || dirEntry.IsDir() {
			return nil
		}

		if strings.ToLower(filepath.Ext(path)) == ".cpp" {
			// 1. Get all includes and the full content of the file
			audit := extractIncludes(path, includeRegex)
			content, _ := os.ReadFile(path)
			fileText := string(content)

			// Clean the file text by removing single-line and multi-line comments
			// This prevents false positives where a symbol is mentioned in a comment
			cleanText := stripComments(fileText)

			// OPTIMIZATION: Get the content of the corresponding header (.h)
			// We check this to see if a symbol was ALREADY forward declared in the header.
			headerPath := strings.TrimSuffix(path, ".cpp") + ".h"				// Convert from Private/XXX.cpp to Public/XXX.h for standard Unreal structure
				if _, err := os.Stat(headerPath); os.IsNotExist(err) {
					altPath := strings.Replace(headerPath, "\\Private\\", "\\Public\\", 1)
					if _, err := os.Stat(altPath); err == nil {
						headerPath = altPath
					}
				}
			headerText := ""
			if _, err := os.Stat(headerPath); err == nil {
				hContent, _ := os.ReadFile(headerPath)
				headerText = string(hContent)
				// Add the header content to the full text to find usage across both files
				cleanText += "\n" + stripComments(headerText)
			}

			fmt.Printf("\n📄 File: %s\n", path)
			summary := FileSummary{Path: path}

			// 2. For each include, check if any of its symbols are used
			for _, include := range audit.Includes {
				// SKIP: If the current file is "MyFile.cpp" and we are looking at "MyFile.h",
				// it is ALWAYS essential to have your own header.
				if strings.TrimSuffix(filepath.Base(path), ".cpp") == strings.TrimSuffix(include, ".h") {
					summary.Includes = append(summary.Includes, IncludeStatus{
						Name:   include,
						Status: "Essential",
					})
					fmt.Printf("   ✅ Essential: #include \"%s\" (Own Header)\n", include)
					continue
				}

				status := "Unknown"
				symbolsInThisHeader := []string{}

				// Find all symbols that "belong" to this included header
				for symbol, headers := range symbolRegistry {
					for _, header := range headers {
						if header == include || filepath.Base(header) == include {
							symbolsInThisHeader = append(symbolsInThisHeader, symbol)
							break
						}
					}
				}

				// Check if any of these symbols appear in the CLEANED .cpp text
				hasAnyFullUsage := false
				hasAnyUsage := false

				// FORWARD DECLARATION OPTIMIZATION:
				alreadyForwardDeclared := false

				for _, symbol := range symbolsInThisHeader {
					// We use a case-sensitive substring check for the quick first pass.
					if strings.Contains(cleanText, symbol) {
						hasAnyUsage = true

						// Check usage in .cpp specifically
						cppContent, _ := os.ReadFile(path)
						cppOnlyText := stripComments(string(cppContent))

						fullUsageRegex := GetFullUsageRegex(symbol, headerText)
						cppFullUsage := fullUsageRegex.MatchString(cppOnlyText)

						if fullUsageRegex.MatchString(cleanText) {
							hasAnyFullUsage = true
						}

						// Check if it was forward declared in the local header
						fwdPattern := fmt.Sprintf(`\b(class|struct|namespace)\s+\b%s\b\s*;`, symbol)
						if headerText != "" && regexp.MustCompile(fwdPattern).MatchString(headerText) {
							if !cppFullUsage {
								alreadyForwardDeclared = true
							}
						}
					}

					// SECOND PASS: Even if the symbol name itself isn't in cleanText,
					// check for mapped variables from the header (e.g. abilitySystemComponent->)
					if !hasAnyFullUsage {
						fullUsageRegex := GetFullUsageRegex(symbol, headerText)
						if fullUsageRegex.MatchString(cleanText) {
							hasAnyUsage = true
							hasAnyFullUsage = true
						}
					}
				}

				// 3. Report if the include seems redundant
				suggestion := ""
				if len(symbolsInThisHeader) > 0 {
					if hasAnyUsage {
						if hasAnyFullUsage {
							status = "Essential"
							fmt.Printf("   ✅ Essential: #include \"%s\"\n", include)
						} else {
							// If it's already forward declared, the include in the .cpp is redundant
							if alreadyForwardDeclared {
								status = "Redundant"
								summary.RedundantCount++
								fmt.Printf("   ⚠️  REDUNDANT: #include \"%s\" (Already forward declared in header)\n", include)
							} else {
								status = "Forward"
								summary.ForwardCount++
								// Suggest forward declaration based on prefix (Unreal Convention)
								prefix := "class"
								if len(symbolsInThisHeader[0]) > 0 && symbolsInThisHeader[0][0] == 'F' {
									prefix = "struct"
								}
								suggestion = fmt.Sprintf("%s %s;", prefix, symbolsInThisHeader[0])
								fmt.Printf("   💡 FORWARD:   #include \"%s\" (Only pointers/refs used. Suggest: %s)\n", include, suggestion)
							}
						}
					} else {
						status = "Redundant"
						summary.RedundantCount++
						fmt.Printf("   ⚠️  REDUNDANT: #include \"%s\" (No symbols from this header used)\n", include)
					}
				} else {
					fmt.Printf("   ❔ Unknown:   #include \"%s\" (Internal/Third Party)\n", include)
				}
				summary.Includes = append(summary.Includes, IncludeStatus{
					Name:             include,
					Status:           status,
					SuggestedForward: suggestion,
				})
			}
			summary.TotalIncludes = len(audit.Includes)
			summaries = append(summaries, summary)
		}
		return nil
	})

	return summaries, err
}

// stripComments removes both // and /* */ comments from C++ source text.
// Input:
//   - text: The full content of a C++ source file.
//
// Output:
//   - A string with all comments replaced by single spaces to preserve layout.
func stripComments(text string) string {
	// Regular expression to match single line // comments and multi-line /* */ comments
	// Use [^\n]* instead of .* to avoid matching across headers/newlines if not handled
	commentRegex := regexp.MustCompile(`(?s)//[^\n]*|/\*.*?\*/`)
	return commentRegex.ReplaceAllString(text, " ")
}

// extractIncludes is a helper that only extracts includes from a file.
// It is faster than scanFile because it skips symbol definition scanning.
//
// Input:
//   - path: The relative or absolute path to the file.
//   - includeRegex: The compiled regex for detecting #include.
//
// Output:
//   - A FileAudit struct containing only the Includes list.
func extractIncludes(path string, includeRegex *regexp.Regexp) FileAudit {
	file, _ := os.Open(path)
	defer file.Close()
	audit := FileAudit{Path: path}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if matches := includeRegex.FindStringSubmatch(line); len(matches) > 1 {
			audit.Includes = append(audit.Includes, matches[1])
		}
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

// generateDashboard creates a simple HTML file to visualize the results.
func generateDashboard(summaries []FileSummary) error {
	// Calculate Global Stats
	totalFiles := len(summaries)
	totalRedundant := 0
	totalForward := 0
	totalIncludes := 0
	for _, s := range summaries {
		totalRedundant += s.RedundantCount
		totalForward += s.ForwardCount
		totalIncludes += s.TotalIncludes
	}

	debtRatio := 0.0
	if totalIncludes > 0 {
		debtRatio = (float64(totalRedundant+totalForward) / float64(totalIncludes)) * 100
	}

	// Read the template file
	templateContent, err := os.ReadFile("template.html")
	if err != nil {
		return fmt.Errorf("could not read template.html: %v", err)
	}

	var resultsHtml strings.Builder
	for _, s := range summaries {
		// Calculate file-specific health
		fileHealth := 100.0
		if s.TotalIncludes > 0 {
			fileHealth = 100.0 - (float64(s.RedundantCount+s.ForwardCount) / float64(s.TotalIncludes) * 100.0)
		}
		healthClass := "health-good"
		if fileHealth < 50 {
			healthClass = "health-bad"
		} else if fileHealth < 80 {
			healthClass = "health-warn"
		}

		resultsHtml.WriteString(fmt.Sprintf("<div class='file'><h2>📄 %s <span class='file-health %s'>%.0f%% Health</span></h2><div class='include-list'>", s.Path, healthClass, fileHealth))
		for _, include := range s.Includes {
			class := strings.ToLower(include.Status)
			resultsHtml.WriteString(fmt.Sprintf("<div class='include'><span class='status %s'>%s</span><span class='name'>#include \"%s\"</span>", class, include.Status, include.Name))
			if include.Status == "Forward" {
				resultsHtml.WriteString(fmt.Sprintf("<span class='suggestion'>💡 Suggest: %s</span>", include.SuggestedForward))
			}
			resultsHtml.WriteString("</div>")
		}
		resultsHtml.WriteString("</div></div>")
	}

	// Replace the placeholders with our generated HTML and stats
	finalHtml := string(templateContent)
	finalHtml = strings.Replace(finalHtml, "<!-- RESULTS_PLACEHOLDER -->", resultsHtml.String(), 1)
	finalHtml = strings.Replace(finalHtml, "{{TOTAL_FILES}}", fmt.Sprintf("%d", totalFiles), 1)
	finalHtml = strings.Replace(finalHtml, "{{TOTAL_REDUNDANT}}", fmt.Sprintf("%d", totalRedundant+totalForward), 1)
	finalHtml = strings.Replace(finalHtml, "{{RATIO}}", fmt.Sprintf("%.1f%%", debtRatio), 1)

	// Write the final dashboard.html
	err = os.WriteFile("dashboard.html", []byte(finalHtml), 0644)
	return err
}

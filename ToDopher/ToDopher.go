// ToDopher is a lightning-fast source code auditor that extracts "TODO",
// "FIXME", and "HACK" comments from your Unreal Engine project.
//
// ToDo + Gopher = ToDopher. Get it? It's a pun. You're welcome...
//
// It provides a centralized dashboard to track technical debt across multiple
// modules, helping teams prioritize cleanup tasks without opening every file.
//
// Features to add:
//   - Multi-threaded file scanning using a Goroutine worker pool.
//   - Support for multiple comment styles (//, /* */, # for Python/Config).
//   - Customizable search tags (e.g., "TODO", "TODO-Dunder", "SUGGESTION", "IDEA").
//   - Web-based dashboard to filter TODOs by priority, file, or author.
//   - Integration with Git to show "Who" added the TODO and "When" (git blame).
//   - Exporting of "Debt Reports" in Markdown for project management.
//
// Common Pitfalls:
//   - Encoding Errors: Large projects may contain non-UTF8 files; handle decoding gracefully.
//   - Binary Files: Accidentally scanning a .uasset or .exe will produce garbage; use extension filters.
//   - Performance: Iterating through tens of thousands of files in "Intermediate/" or "Plugins/".
//   - Context: Capturing only the TODO line isn't enough; capture 2-3 lines of surrounding code.
//
// Note: This tool works best when integrated into a pre-commit or CI/CD workflow.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Config holds the scanner settings
type Config struct {
	SearchTags        []string
	IgnoreFolders     []string
	AllowedExtensions []string
}

// Finding represents a single technical debt entry found in the source code.
// The 'json:"file"' parts tell the JSON encoder how to name these fields when converting to JSON, which is useful for the API endpoint.
type Finding struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Tag     string `json:"tag"`
	Author  string `json:"author"`
	Content string `json:"content"`
}

func main() {
	// Step 1: Initialize configuration with default search tags, ignored folders, and allowed extensions
	config := Config{
		SearchTags:        []string{"TODO", "FIXME", "HACK", "BUG", "SUGGESTION", "IDEA", "REWORK"},
		IgnoreFolders:     []string{"Intermediate", "ThirdParty", ".git", "Binaries", "Saved", "Plugins"},
		AllowedExtensions: []string{".h", ".cpp", ".html", ".go", ".java", ".py", ".ini", ".cs"},
	}

	fmt.Println("📝 ToDopher is calculating technical debt...")

	// Get search directory from arguments, default to current directory
	searchDir := "."
	if len(os.Args) > 1 {
		searchDir = os.Args[1]
	}

	// Step 2: Walk the project directory and collect files to scan
	filesToScan, err := discoverFiles(searchDir, config)
	if err != nil {
		fmt.Printf("Error walking the path: %v\n", err)
		return
	}

	fmt.Printf("Found %d files to scan. Commencing concurrent audit...\n", len(filesToScan))

	// Step 3 & 4: Concurrent Scanning Engine with Regex Matcher
	findings := startWorkerPool(filesToScan, config)

	fmt.Printf("Audit complete! Total findings across all files: %d\n", len(findings))

	// Step 5: Generate static HTML report
	err = generateHtmlReport(findings, "report.html")
	if err != nil {
		fmt.Printf("Error generating report: %v\n", err)
		return
	}

	if absPath, err := filepath.Abs("report.html"); err == nil {
		fmt.Printf("📊 Report generated successfully at:\n %s\n", absPath)
	} else {
		fmt.Println("📊 Report generated successfully at:\n report.html")
	}
}

// generateHtmlReport creates a standalone HTML file containing the audit findings by injecting data into a template.
//
// Parameters:
//   - findings: A slice of Finding structs to be included in the report.
//   - outputPath: The file path where the HTML report will be saved.
//
// Returns:
//   - error: Any error encountered during file creation or template execution.
func generateHtmlReport(findings []Finding, outputPath string) error {
	jsonData, err := json.Marshal(findings)
	if err != nil {
		return fmt.Errorf("failed to marshal findings to JSON: %w", err)
	}

	// Prepare data for the template
	data := struct {
		FindingsJSON template.JS
	}{
		FindingsJSON: template.JS(jsonData),
	}

	// Read and parse the template file
	tmpl, err := template.ParseFiles("template.html")
	if err != nil {
		return fmt.Errorf("failed to parse template file: %w", err)
	}

	// Create the output file
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create report file: %w", err)
	}
	defer file.Close()

	// Execute the template and write to the file
	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	fmt.Printf("📊 Report generated successfully at %s\n", outputPath)
	return nil
}

// discoverFiles traverses the file tree starting at searchDir and returns a list of files matching scan criteria.
//
// Parameters:
//   - searchDir: The root directory to start the traversal from.
//   - config: The Config struct containing ignores and allowed extensions.
//
// Returns:
//   - []string: A slice containing paths to all files identified for auditing.
//   - error: Any error encountered during directory traversal.
func discoverFiles(searchDir string, config Config) ([]string, error) {
	var filesToScan []string
	err := filepath.WalkDir(searchDir, func(path string, dir os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Only consider files that are not in ignored folders and have allowed extensions
		if !dir.IsDir() && shouldScan(path, config) {
			filesToScan = append(filesToScan, path)
		}
		return nil
	})
	return filesToScan, err
}

// startWorkerPool initializes a pool of worker goroutines to scan files concurrently.
// It distributes the file paths across workers and aggregates the results.
//
// Parameters:
//   - filesToScan: A slice of strings containing the paths of files to be audited.
//   - config: The Config struct containing SearchTags and other scanner settings.
//
// Returns:
//   - []Finding: A slice of all findings discovered across all scanned files.
func startWorkerPool(filesToScan []string, config Config) []Finding {
	const numWorkers = 20
	// Create channels for job distribution and result collection
	fileJobs := make(chan string, len(filesToScan))
	results := make(chan []Finding, len(filesToScan))
	var waitGroup sync.WaitGroup

	// Compile the case-insensitive regex once for performance.
	// This pattern searches for:
	// 		Group 1: One of the search tags (e.g., TODO, FIXME)
	//	 	Group 2: An optional author name following a hyphen (e.g., TODO-Dunder)
	// 		Group 3: A colon followed by any amount of whitespace
	// 		Group 4: The remaining text on the line as the description/content
	pattern := fmt.Sprintf(`(?i)(%s)(?:-([A-Z]+))?:\s*(.*)`, strings.Join(config.SearchTags, "|"))
	regex := regexp.MustCompile(pattern)

	// Spawn workerIndex goroutines
	for workerIndex := 1; workerIndex <= numWorkers; workerIndex++ {
		// Increment the wait group counter which tracks active workers so we can wait for them to finish
		waitGroup.Add(1)

		// Each worker will read from the jobs channel, process the file, and send results back
		// go func() creates a new goroutine for each worker, allowing them to run concurrently without blocking the main thread
		go func() {
			// Done will be called when this worker finishes its work, decrementing the wait group counter
			defer waitGroup.Done()
			// Range over the jobs channel. Each job is a file path to scan.
			for path := range fileJobs {
				findings := scanFile(path, regex)
				results <- findings
			}
		}()
	}

	// Send files to the worker pool through the jobs channel. Each file path is a job for the workers to process.
	for _, path := range filesToScan {
		fileJobs <- path
	}
	close(fileJobs)

	// Wait for all workers to finish and close results channel in a separate goroutine
	go func() {
		waitGroup.Wait()
		close(results)
	}()

	var allFindings []Finding
	for findings := range results {
		allFindings = append(allFindings, findings...)
	}

	return allFindings
}

// shouldScan evaluates if a file should be audited based on its path and the provided configuration.
// It returns true if the file extension is allowed and the file is not within an ignored folder.
//
// Parameters:
//   - path: The absolute or relative path to the file.
//   - config: The Config struct containing AllowedExtensions and IgnoreFolders.
//
// Returns:
//   - bool: True if the file matches scanning criteria, false otherwise.
func shouldScan(path string, config Config) bool {
	// Skip ignored folders
	for _, folder := range config.IgnoreFolders {
		if strings.Contains(path, string(os.PathSeparator)+folder+string(os.PathSeparator)) {
			return false
		}
	}

	// Filter by extension
	ext := filepath.Ext(path)
	for _, allowed := range config.AllowedExtensions {
		if ext == allowed {
			return true
		}
	}
	return false
}

// scanFile reads a file line-by-line and identifies lines containing technical debt tags using regex.
//
// Parameters:
//   - filePath: The path of the file to scan.
//   - re: The compiled regular expression for identifying tags and authors.
//
// Returns:
//   - []Finding: A slice of Finding structs discovered in this file.
func scanFile(filePath string, regex *regexp.Regexp) []Finding {
	file, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("Error opening file %s: %v\n", filePath, err)
		return nil
	}
	defer file.Close()

	var findings []Finding
	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		// Read the current line of the file
		line := scanner.Text()
		// Use the precompiled regex to find matches in the current line.
		matches := regex.FindStringSubmatch(line)
		// If matches are found, create a Finding struct with the relevant information and add it to the findings slice.
		if len(matches) > 0 {
			finding := Finding{
				File: filePath,
				Line: lineNum,
				// matches[1] contains the tag (e.g., TODO),
				// matches[2] contains the optional author (e.g., Dunder), and
				// matches[3] contains the content of the comment.
				Tag:     strings.ToUpper(matches[1]),
				Author:  matches[2],
				Content: strings.TrimSpace(matches[3]),
			}
			findings = append(findings, finding)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading file %s: %v\n", filePath, err)
		return nil
	}
	return findings
}

/*
   DETAILED IMPLEMENTATION PLAN
   ----------------------------

   1. SEARCH PATTERNS & CONFIG
      - Support common formats: "TODO", "FIXME", "HACK", "BUG".
      - Support personalized tags: "TODO-Dunder", "TODO-TeamName".
      - Support informational tags: "SUGGESTION", "IDEA", "QUESTION", "REWORK".
      - Use a slice of strings `searchTags` to allow the user to add more via CLI.

   2. PROJECT DISCOVERY & FILTERS
      - Walk "Source/", "Plugins/", and "Config/" directories.
      - Implement an "Ignore List" for "Intermediate/", "ThirdParty/", and ".git/".
      - Filter by extension (.h, .cpp, .cs, .py, .ini).

   3. CONCURRENT SCANNING ENGINE
      - Use a `sync.WaitGroup` and a channel of `string` (file paths).
      - Spawn 10-20 workers to read files concurrently.
      - Use `bufio.Scanner` to read files line-by-line for low memory footprint.

   4. REGEX MATCHER
      - Use a case-insensitive regex: `(?i)(TODO|FIXME|HACK|BUG|SUGGESTION|IDEA|REWORK)(-[A-Z]+)?:\s*(.*)`.
      - For every match, store: { File, LineNum, Content, Type, Author (optional) }.

   5. DASHBOARD (WEB UI)
      - Use `net/http` to serve a simple HTML/JS frontend.
      - Send results via a single JSON endpoint `/api/todos`.
      - Use a JS data table (like DataTables.net) for instant sorting and filtering.
*/

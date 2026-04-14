// ToDopher is a lightning-fast source code auditor that extracts "TODO",
// "FIXME", and "HACK" comments from your Unreal Engine project.
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
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	// DefaultTemplatePath is the source HTML file used to generate the report.
	DefaultTemplatePath = "template.html"
	// DefaultOutputPath is the filename for the generated technical debt report.
	DefaultOutputPath = "report.html"
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
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Tag     string   `json:"tag"`
	Author  string   `json:"author"`
	Content string   `json:"content"`
	Context []string `json:"context"`
	When    string   `json:"when"`
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

	// Filter out the report file itself and the template to avoid self-scanning
	var filteredFiles []string
	for _, f := range filesToScan {
		base := filepath.Base(f)
		if base != DefaultOutputPath && base != DefaultTemplatePath {
			filteredFiles = append(filteredFiles, f)
		}
	}
	filesToScan = filteredFiles

	fmt.Printf("Found %d files to scan. Commencing concurrent audit...\n", len(filesToScan))

	// Step 3 & 4: Concurrent Scanning Engine with Regex Matcher
	findings := startWorkerPool(filesToScan, config)

	// Clear progress line before moving to the summary
	// I think it actually looks better to keep the progress bar and just print the summary on a new line. This might change.
	//fmt.Print("\r\033[K")

	fmt.Printf("\nAudit complete! Total findings across all files: %d\n", len(findings))

	// Step 5: Generate static HTML report
	err = generateHtmlReport(findings, DefaultOutputPath)
	if err != nil {
		fmt.Printf("Error generating report: %v\n", err)
		return
	}

	if absPath, err := filepath.Abs(DefaultOutputPath); err == nil {
		fmt.Printf("📊 Report generated successfully at:\n %s\n", absPath)
	} else {
		fmt.Println("📊 Report generated successfully at:\n " + DefaultOutputPath)
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
	// Convert findings to JSON for embedding in the template
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
	tmpl, err := template.ParseFiles(DefaultTemplatePath)
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
	totalFiles := len(filesToScan)

	// Create channels for job distribution and result collection
	fileJobs := make(chan string, totalFiles)
	results := make(chan []Finding, totalFiles)
	var waitGroup sync.WaitGroup

	// Compile the case-insensitive regex once for performance.
	// This pattern searches for:
	// 		Prefix: Supports // (C++/Go), # (Python/Shell), /* (Block Start), or * (Block Middle)
	// 		Group 1: One of the search tags (e.g., TODO, FIXME)
	//	 	Group 2: An optional author name following a hyphen (e.g., TODO-Dunder)
	// 		Group 3: A colon followed by any amount of whitespace
	// 		Group 4: The remaining text on the line as the description/content
	pattern := fmt.Sprintf(`(?i)(?://|#|/\*|\*)\s*(%s)(?:-([A-Z]+))?:\s*(.*)`, strings.Join(config.SearchTags, "|"))
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

	// Result collector and Progress Bar logic
	var allFindings []Finding
	count := 0
	done := make(chan bool)

	// This goroutine collects results from the workers and updates the progress bar.
	go func() {
		for findings := range results {
			// The ... tells Go to "unpack" the slice and append each element individually to allFindings.
			allFindings = append(allFindings, findings...)
			count++
			printProgressBar(count, totalFiles)
		}
		done <- true
	}()

	// Wait for all workers to finish and close results channel
	waitGroup.Wait()
	close(results)

	// Wait for result collector to finish aggregating all data
	<-done

	return allFindings
}

// printProgressBar renders a simple ASCII progress bar in the terminal to provide visual feedback
// during the file scanning process. It uses carriage returns (\r) to update the same line.
//
// Parameters:
//   - current: The number of files processed so far.
//   - total: The total number of files to be scanned.
func printProgressBar(current, total int) {
	width := 40
	percent := float64(current) / float64(total)
	filled := int(percent * float64(width))

	bar := "["
	// The loop constructs the visual representation of the progress bar.
	// It fills the bar with "=" characters for completed portions, a ">" character for the current position, and spaces for the remaining portion.
	for i := 0; i < width; i++ {
		if i < filled {
			bar += "="
		} else if i == filled {
			bar += ">"
		} else {
			bar += " "
		}
	}
	bar += "]"

	fmt.Printf("\rScanning: %s %d/%d (%d%%)", bar, current, total, int(percent*100))
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

// scanFile reads a file and identifies lines containing technical debt tags.
// It also captures 3 lines of following context for each finding.
//
// Parameters:
//   - filePath: The path of the file to scan.
//   - regex: The compiled regular expression for identifying tags and authors.
//
// Returns:
//   - []Finding: A slice of Finding structs discovered in this file.
func scanFile(filePath string, regex *regexp.Regexp) []Finding {
	content, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Printf("Error reading file %s: %v\n", filePath, err)
		return nil
	}

	lines := strings.Split(string(content), "\n")
	var findings []Finding

	for i, line := range lines {
		matches := regex.FindStringSubmatch(line)
		if len(matches) > 0 {
			lineNum := i + 1
			// Determine context (up to 3 lines after the tag)
			var contextLines []string
			for j := 1; j <= 3 && i+j < len(lines); j++ {
				// TrimRight is used to remove any trailing newline characters from the context lines, ensuring cleaner output in the report.
				contextLines = append(contextLines, strings.TrimRight(lines[i+j], "\r"))
			}

			author := matches[2]
			when := ""

			// If no author extracted from tag, try git blame
			if author == "" {
				blameAuthor, blameDate := gitBlame(filePath, lineNum)
				if blameAuthor != "" {
					author = blameAuthor
				}
				when = blameDate
			}

			finding := Finding{
				File:    filePath,
				Line:    lineNum,
				Tag:     strings.ToUpper(matches[1]),
				Author:  author,
				Content: strings.TrimSpace(matches[3]),
				Context: contextLines,
				When:    when,
			}
			findings = append(findings, finding)
		}
	}
	return findings
}

// gitBlame uses the 'git blame' command to identify the author of a specific line in a file.
// It leverages the --porcelain flag to parse machine-readable metadata from the Git history.
//
// TODO We should show who was the first author to add the line with a TODO, not the last one.
// We can use "git log -L <line>,<line>:<file>" for that, but it is more complex to parse.
//
// Parameters:
//   - filePath: The path to the file to perform the blame on.
//   - line: The specific line number to investigate.
//
// Returns:
//   - string: The name of the author who last modified the line.
//   - string: Reserved for the commit date (currently returns empty).
func gitBlame(filePath string, line int) (string, string) {
	// git blame -L <line>,<line> --porcelain <file>
	cmd := exec.Command("git", "blame", "-L", fmt.Sprintf("%d,%d", line, line), "--porcelain", filePath)

	// CombinedOutput runs the command and returns its combined standard output and standard error. If the command fails, it returns an error.
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", ""
	}

	// The output of 'git blame --porcelain' contains multiple lines of metadata. We need to parse it to find the author and date.
	lines := strings.Split(string(output), "\n")
	author := ""

	// The 'author' line in the output looks like: "author John Doe". We can extract the author's name by looking for this line.
	for _, line := range lines {
		if strings.HasPrefix(line, "author ") {
			author = strings.TrimPrefix(line, "author ")
			break
		}
	}

	return author, ""
}

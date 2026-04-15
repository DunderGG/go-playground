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
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed assets/*
var content embed.FS

const (
	// DefaultOutputPath is the filename for the generated technical debt report.
	DefaultOutputPath = "report.html"
	// TagRegexPattern is core regex for identifying technical debt tags.
	// 		Prefix: Supports // (C++/Go), # (Python/Shell), /* (Block Start), or * (Block Middle)
	// 		Group 1: Placeholder for search tags (e.g., TODO, FIXME)
	//	 	Group 2: An optional author name following a hyphen (e.g., TODO-Dunder)
	// 		Group 3: A colon followed by any amount of whitespace
	// 		Group 4: The remaining text on the line as the description/content
	TagRegexPattern = `(?i)(?://|#|/\*|\*)\s*(%s)(?:-([A-Z]+))?:\s*(.*)`
)

var (
	// IsQuiet mode flag for suppressing non-essential output
	IsQuiet bool
	// OutputPath is the destination for the HTML report
	OutputPath string
	// CustomTags is a comma-separated list of search tags
	CustomTags string
	// CustomExtensions is a comma-separated list of file extensions
	CustomExtensions string
	// CustomIgnore is a comma-separated list of folders to ignore
	CustomIgnore string
	// JsonPath is the destination for the JSON export
	JsonPath string
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
	Status  string   `json:"status"` // "Fresh", "Stale", or "Ancient"
}

func main() {
	parseFlags()
	config := initializeConfig()

	if !IsQuiet {
		printIntro()
	}

	// Get search directory from arguments, default to current directory
	searchDir := "."
	if args := flag.Args(); len(args) > 0 {
		searchDir = args[0]
	}

	// Walk the project directory and collect files to scan
	filesToScan, err := discoverFiles(searchDir, config)
	if err != nil {
		fmt.Printf("Error walking the path: %v\n", err)
		return
	}

	filesToScan = filterOutputFiles(filesToScan)

	if !IsQuiet {
		fmt.Printf("Found %d files to scan. Commencing concurrent audit...\n", len(filesToScan))
	}

	// Concurrent Scanning Engine with Regex Matcher
	findings := startWorkerPool(filesToScan, config)

	if !IsQuiet {
		fmt.Printf("\nAudit complete! Total findings across all files: %d\n", len(findings))
	}

	generateReports(findings)
}

// parseFlags defines and parses command-line flags for customizing the tool's behavior.
func parseFlags() {
	// Custom usage message
	flag.Usage = func() {
		printIntro()
		fmt.Println("Usage: ToDopher [options] [search_directory]")
		fmt.Println("\nToDopher is a high-speed technical debt scanner that extracts TODOs from source code.")
		fmt.Println("\nOptions:")
		fmt.Println("  -e, --exts string    Comma-separated list of additional file extensions")
		fmt.Println("  -h, --help           Show this help message")
		fmt.Println("  -i, --ignore string  Comma-separated list of additional folders to ignore")
		fmt.Println("  -j, --json string    Optional path to export findings as a JSON file")
		fmt.Println("  -o, --output string  Output filename for the report (default \"report.html\")")
		fmt.Println("  -q, --quiet          Quiet mode (suppress output)")
		fmt.Println("  -t, --tags string    Comma-separated list of additional search tags")

		fmt.Println("\nExamples:")
		fmt.Println("   ToDopher .                               # Scan current directory")
		fmt.Println("   ToDopher -q 	                            # Quiet mode")
		fmt.Println("   ToDopher -o \"report.html\" -j \"data.json\" # Custom outputs")
		fmt.Println("   ToDopher -t \"BUG,URGENT\" -e \".js,.ts\"    # Custom tags and extensions")
		fmt.Println("   ToDopher -i \"node_modules,dist\"          # Custom ignore folders")
	}

	// The -q or --quiet flag enables quiet mode, which suppresses non-essential output for a cleaner experience when the user just wants the report.
	flag.BoolVar(&IsQuiet, "q", false, "Quiet mode (suppress output)")
	flag.BoolVar(&IsQuiet, "quiet", false, "Quiet mode (suppress output)")
	// The -o or --output flag allows the user to specify a custom file path for the generated report, defaulting to "report.html" if not provided.
	flag.StringVar(&OutputPath, "o", DefaultOutputPath, "Output filename for the report")
	flag.StringVar(&OutputPath, "output", DefaultOutputPath, "Output filename for the report")
	// The -t or --tags flag allows the user to append custom search tags (e.g., -t "SECURITY,IMPORTANT").
	flag.StringVar(&CustomTags, "t", "", "Comma-separated list of additional search tags")
	flag.StringVar(&CustomTags, "tags", "", "Comma-separated list of additional search tags")
	// The -e or --exts flag allows the user to append additional file extensions (e.g., -e ".js,.ts").
	flag.StringVar(&CustomExtensions, "e", "", "Comma-separated list of additional file extensions")
	flag.StringVar(&CustomExtensions, "exts", "", "Comma-separated list of additional file extensions")
	// The -i or --ignore flag allows the user to append additional folders to ignore (e.g., -i "node_modules,dist").
	flag.StringVar(&CustomIgnore, "i", "", "Comma-separated list of additional folders to ignore")
	flag.StringVar(&CustomIgnore, "ignore", "", "Comma-separated list of additional folders to ignore")
	// The -j or --json flag allows the user to specify a path for a JSON export of the findings.
	flag.StringVar(&JsonPath, "j", "", "Optional path to export findings as a JSON file")
	flag.StringVar(&JsonPath, "json", "", "Optional path to export findings as a JSON file")
	flag.Parse()
}

// initializeConfig sets up and returns the Config struct with default values
// and incorporates any custom tags or extensions provided via command-line flags.
func initializeConfig() Config {
	// Initialize configuration with default search tags, ignored folders, and allowed extensions
	config := Config{
		SearchTags:        []string{"TODO", "FIXME", "HACK", "BUG", "SUGGESTION", "IDEA", "REWORK"},
		IgnoreFolders:     []string{"Intermediate", "ThirdParty", ".git", "Binaries", "Saved", "Plugins"},
		AllowedExtensions: []string{".h", ".cpp", ".html", ".go", ".java", ".py", ".ini", ".cs"},
	}

	// Append custom tags if provided via CLI
	if CustomTags != "" {
		tags := strings.Split(CustomTags, ",")
		for _, tag := range tags {
			trimmed := strings.TrimSpace(tag)
			if trimmed != "" {
				config.SearchTags = append(config.SearchTags, trimmed)
			}
		}
	}

	// Append custom extensions if provided via CLI
	if CustomExtensions != "" {
		exts := strings.Split(CustomExtensions, ",")
		for _, ext := range exts {
			trimmed := strings.TrimSpace(ext)
			if trimmed != "" {
				// Ensure it starts with a dot
				if !strings.HasPrefix(trimmed, ".") {
					trimmed = "." + trimmed
				}
				config.AllowedExtensions = append(config.AllowedExtensions, trimmed)
			}
		}
	}

	// Append custom ignore folders if provided via CLI
	if CustomIgnore != "" {
		folders := strings.Split(CustomIgnore, ",")
		for _, folder := range folders {
			trimmed := strings.TrimSpace(folder)
			if trimmed != "" {
				config.IgnoreFolders = append(config.IgnoreFolders, trimmed)
			}
		}
	}
	return config
}

// filterOutputFiles removes the report file from the list of files to scan to prevent self-scanning.
//
// Parameters:
//   - files: A slice of strings containing all discovered file paths.
//
// Returns:
//   - []string: A filtered slice of file paths excluding the HTML and JSON output files.
func filterOutputFiles(files []string) []string {
	// Filter out the report file itself to avoid self-scanning
	var filteredFiles []string
	for _, file := range files {
		base := filepath.Base(file)
		if base != OutputPath {
			filteredFiles = append(filteredFiles, file)
		}
	}
	return filteredFiles
}

// generateReports coordinates the generation of all requested audit reports.
// It handles both the visual HTML dashboard and the machine-readable JSON export.
//
// Parameters:
//   - findings: A slice of Finding structs containing the audit results.
func generateReports(findings []Finding) {
	// Generate static HTML report
	err := generateHtmlReport(findings, OutputPath)
	if err != nil {
		fmt.Printf("Error generating report: %v\n", err)
	}

	// Export to JSON if requested
	if JsonPath != "" {
		exportToJson(findings)
	}

	if absPath, err := filepath.Abs(OutputPath); err == nil {
		if !IsQuiet {
			fmt.Printf("📊 Report generated successfully at:\n %s\n", absPath)
		}
	} else {
		if !IsQuiet {
			fmt.Println("📊 Report generated successfully at:\n " + OutputPath)
		}
	}
}

// exportToJson serializes the findings into a pretty-printed JSON file.
//
// Parameters:
//   - findings: A slice of Finding structs to be exported.
func exportToJson(findings []Finding) {
	jsonData, err := json.MarshalIndent(findings, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling JSON: %v\n", err)
		return
	}

	// WriteFile is a convenience function that writes data to a file, creating it if it doesn't exist or truncating it if it does.
	// 0644 sets the file permissions to be readable and writable by the owner, and readable by others.
	err = os.WriteFile(JsonPath, jsonData, 0644)
	if err != nil {
		fmt.Printf("Error writing JSON file: %v\n", err)
	} else if !IsQuiet {
		if absJson, err := filepath.Abs(JsonPath); err == nil {
			fmt.Printf("📄 JSON data exported to:\n %s\n", absJson)
		} else {
			fmt.Printf("📄 JSON data exported to:\n %s\n", JsonPath)
		}
	}
}

// extractAssets copies the embedded assets/ folder to the local disk.
// This is necessary because report.html references local files like assets/jquery.min.js.
func extractAssets() error {
	return fs.WalkDir(content, "assets", func(path string, dir fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// MkdirAll will create the directory if it doesn't exist, and do nothing if it already exists.
		// 0755 allows the owner to read/write/execute and others to read/execute.
		if dir.IsDir() {
			return os.MkdirAll(path, 0755)
		}

		// Read the embedded file data
		data, err := content.ReadFile(path)
		if err != nil {
			return err
		}

		// Write it to the local disk
		// 0644 allows the owner to read/write and others to read.
		return os.WriteFile(path, data, 0644)
	})
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

	// Compile the case-insensitive regex once for performance using the global pattern.
	pattern := fmt.Sprintf(TagRegexPattern, strings.Join(config.SearchTags, "|"))
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
			if !IsQuiet {
				printProgressBar(count, totalFiles)
			}
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

	for lineCounter, line := range lines {
		matches := regex.FindStringSubmatch(line)
		if len(matches) > 0 {
			lineNum := lineCounter + 1
			// Determine context (up to 3 lines after the tag)
			var contextLines []string
			// The loop captures up to 3 lines of context following the line where the tag was found, ensuring we don't go out of bounds.
			for contextCounter := 1; contextCounter <= 3 && lineCounter+contextCounter < len(lines); contextCounter++ {
				// TrimRight is used to remove any trailing newline characters from the context lines, ensuring cleaner output in the report.
				contextLines = append(contextLines, strings.TrimRight(lines[lineCounter+contextCounter], "\r"))
			}

			// matches[1] is the tag (e.g., TODO),
			// matches[2] is the optional author, and
			// matches[3] is the content after the tag.
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

			// Calculate Status based on age
			status := "Fresh"
			if when != "" {
				if commitTime, err := time.Parse("2006-01-02", when); err == nil {
					daysOld := time.Since(commitTime).Hours() / 24
					if daysOld > 180 {
						status = "Ancient"
					} else if daysOld > 30 {
						status = "Stale"
					}
				}
			}

			finding := Finding{
				File:    filePath,
				Line:    lineNum,
				Tag:     strings.ToUpper(matches[1]),
				Author:  author,
				Content: strings.TrimSpace(matches[3]),
				Context: contextLines,
				When:    when,
				Status:  status,
			}
			findings = append(findings, finding)
		}
	}
	return findings
}

// gitBlame uses the 'git log' command to find the very first person who introduced
// the line containing the technical debt tag.
//
// Parameters:
//   - filePath: The path to the file to investigate.
//   - line: The current line number.
//
// Returns:
//   - string: The name of the original author.
//   - string: The date (YYYY-MM-DD) when the line was first introduced.
func gitBlame(filePath string, line int) (string, string) {
	absPath, _ := filepath.Abs(filePath)
	dir := filepath.Dir(absPath)

	// rangeArg specifies the line range to trace in the format "start,end:file".
	// In this case, we want to trace just the single line where the TODO was found, so start and end are the same.
	// The filepath.Base is used to get just the filename, which is required by the -L option.
	rangeArg := fmt.Sprintf("%d,%d:%s", line, line, filepath.Base(absPath))
	// exec.Command is used to run the git log command with the specified arguments.
	// -L tells git to trace the history of the specified line range,
	// --reverse ensures we get the earliest commit, and
	// --pretty=format specifies the output format to include the author name and date separated by a pipe character.
	// --no-patch ensures we only get commit metadata without the diff.
	cmd := exec.Command("git", "log", "-L", rangeArg, "--reverse", "--pretty=format:%an|%as", "--max-count=1", "--no-patch")
	cmd.Dir = dir

	// CombinedOutput runs the command and captures both stdout and stderr.
	// If the command fails (e.g., if the line is uncommitted), it returns an error,
	// which we catch to trigger the fallback blame method.
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fallbackBlame(filePath, line)
	}

	// The output is expected to be in the format "Author Name|YYYY-MM-DD".
	// We split it by the pipe character to extract the author and date.
	parts := strings.Split(strings.TrimSpace(string(output)), "|")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}

	// If the output format is unexpected or if there was an error,
	// we fall back to a standard git blame on the current state of the file.
	return fallbackBlame(filePath, line)
}

// fallbackBlame is used when the deep history trace fails (e.g. for uncommitted local changes).
// It performs a standard 'git blame' on the current state of the file.
//
// Parameters:
//   - filePath: The path to the file to perform the blame on.
//   - line: The specific line number to investigate.
//
// Returns:
//   - string: The name of the author who last modified the line.
//   - string: The date associated with the last modification (if available).
func fallbackBlame(filePath string, line int) (string, string) {
	absPath, _ := filepath.Abs(filePath)
	dir := filepath.Dir(absPath)

	// Run 'git blame' on the specified line of the file,
	// using the --porcelain option to get detailed information about the commit that last modified that line.
	cmd := exec.Command("git", "blame", "-L", fmt.Sprintf("%d,%d", line, line), "--porcelain", filepath.Base(absPath))
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "Unknown", ""
	}

	lines := strings.Split(string(output), "\n")
	author := "Unknown"
	date := ""

	// The porcelain output includes lines that start with "author " and "author-time "
	// which we can parse to get the author's name and the timestamp of the last modification.
	for _, line := range lines {
		if strings.HasPrefix(line, "author ") {
			author = strings.TrimPrefix(line, "author ")
		}

		if strings.HasPrefix(line, "author-time ") {
			// Basic conversion or skip for now if complex, but porcelain gives us data
			timestamp := strings.TrimPrefix(line, "author-time ")
			// Convert the timestamp to a human-readable date format (YYYY-MM-DD)
			if ts, err := strconv.ParseInt(timestamp, 10, 64); err == nil {
				date = time.Unix(ts, 0).Format("2006-01-02")
			}
		}
	}
	return author, date
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
		return fmt.Errorf("Failed to marshal findings to JSON: %w", err)
	}

	// Prepare data for the template
	data := struct {
		FindingsJSON template.JS
	}{
		FindingsJSON: template.JS(jsonData),
	}

	// Read and parse the template from the embedded file system
	tmpl, err := template.ParseFS(content, "assets/template.html")
	if err != nil {
		return fmt.Errorf("Failed to parse embedded template: %w", err)
	}

	// After generating the HTML, we also need to ensure the assets folder exists
	// alongside the report.html on the user's machine so the dashboard can load them.
	// This function extracts the embedded assets/ folder to the physical disk.
	if err := extractAssets(); err != nil {
		return fmt.Errorf("Failed to extract assets: %w", err)
	}

	// Create the output file
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("Failed to create report file: %w", err)
	}
	defer file.Close()

	// Execute the template and write to the file
	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("Failed to execute template: %w", err)
	}

	return nil
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

// printIntro displays an ASCII art banner and introductory message when the tool is run in non-quiet mode.
func printIntro() {
	// Yes the ASCII art is a bit scuffed... deal with it.
	fmt.Println("======================================================")
	intro := `
   ______ ___  ____  ____  ____  __ __  ______ ____
  /_  __/ __ \/ __ \/ __ \/ __ \/ // / / ____/ __  \
   / / / / / / / / / / / / /_/ / _  / / __/ / /_/  /
  / / / /_/ / /_/ / /_/ / ____/ // / / /___/__/ ,_/ 
 /_/  \____/_____/\____/_/   /_//_/ /_____/_/ |_|
`
	fmt.Println(intro)
	fmt.Println("======================================================")
}

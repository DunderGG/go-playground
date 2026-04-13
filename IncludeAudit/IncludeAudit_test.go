package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStripComments verifies that our C++ comment removal logic works correctly.
// It ensures that symbols hidden inside comments don't cause false positives.
func TestStripComments(t *testing.T) {
	// Define the test scenarios: input code -> expected output after stripping
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Single Line", "void Test() { // comment\n}", "void Test() {  }"},
		{"Multi Line", "void /* comment */ Test()", "void   Test()"},
		{"Mixed", "A /* B */ C // D\nE", "A   C  \nE"},
		{"No Comments", "void Test()", "void Test()"},
		{"Multiple Multi-line", "/* A */ B /* C */", "  B  "},
	}

	for _, test := range tests {
		// t.Run creates a sub-test for each scenario in our table
		t.Run(test.name, func(t *testing.T) {
			got := stripComments(test.input)

			// We clean all whitespace (newlines/spaces) for the comparison
			// because the important part is that the CODE remains, not the formatting.
			cleanGot := strings.Map(func(r rune) rune {
				if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
					return -1
				}
				return r
			}, got)
			cleanExpected := strings.Map(func(r rune) rune {
				if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
					return -1
				}
				return r
			}, test.expected)

			if cleanGot != cleanExpected {
				t.Errorf("stripComments(%q)\ngot:  %q\nwant: %q", test.input, got, test.expected)
			}
		})
	}
}

// TestSymbolRegex checks that our "Pass 1" scanner can correctly find
// Class, Struct, and Namespace definitions even with Unreal API macros.
func TestSymbolRegex(t *testing.T) {

	tests := []struct {
		name           string
		line           string
		expectedSymbol string
	}{
		{"Simple Class", "class APlayerCharacter {", "APlayerCharacter"},
		{"Class with API", "class UTILITY_API Logger : public Base {", "Logger"}, // Tests Unreal API macro handling
		{"Struct", "struct FPlayerData {", "FPlayerData"},
		{"Namespace", "namespace PlayerUtils {", "PlayerUtils"},
		{"Enum", "enum EGameState {", "EGameState"},
		{"UCLASS (Should not match)", "UCLASS()", ""}, // Ensures we don't pick up Unreal macros as symbols
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matches := SymbolRegex.FindStringSubmatch(test.line)
			var got string
			if len(matches) > 2 {
				got = matches[2] // Capture Group index 2 is the symbol name
			}
			if got != test.expectedSymbol {
				t.Errorf("symbolRegex match %q: got %q, want %q", test.line, got, test.expectedSymbol)
			}
		})
	}
}

// TestScanFile verifies that the scanner correctly extracts both includes and symbols from a file.
func TestScanFile(t *testing.T) {
	tests := []struct {
		name             string
		content          string
		expectedIncludes []string
		expectedSymbols  []string
	}{
		{
			"Simple Header",
			"#include \"Other.h\"\nclass ATestActor {};",
			[]string{"Other.h"},
			[]string{"ATestActor"},
		},
		{
			"Multiple Elements",
			"#include \"A.h\"\n#include \"B.h\"\nstruct FMyStruct {};\nclass PROJECT_API APlayer {};",
			[]string{"A.h", "B.h"},
			[]string{"FMyStruct", "APlayer"},
		},
		{
			"Empty File",
			"// Just a comment",
			[]string{},
			[]string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpFile, err := os.CreateTemp("", "test_scan_*.h")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(tmpFile.Name())

			if _, err := tmpFile.Write([]byte(test.content)); err != nil {
				t.Fatal(err)
			}
			tmpFile.Close()

			audit := scanFile(tmpFile.Name(), IncludeRegex, SymbolRegex)

			// Helper to compare slices
			compareSlices := func(got, want []string, field string) {
				if len(got) != len(want) {
					t.Errorf("%s length mismatch: got %d (%v), want %d (%v)", field, len(got), got, len(want), want)
					return
				}
				for i := range got {
					if got[i] != want[i] {
						t.Errorf("%s[%d] mismatch: got %q, want %q", field, i, got[i], want[i])
					}
				}
			}

			compareSlices(audit.Includes, test.expectedIncludes, "Includes")
			compareSlices(audit.Symbols, test.expectedSymbols, "Symbols")
		})
	}
}

// TestAnalyzeCppFilesLogic Integration Test verifies the full two-pass logic.
func TestAnalyzeCppFilesLogic(t *testing.T) {
	tests := []struct {
		name             string
		registry         map[string][]string
		cppContent       string
		expectedStatuses map[string]string
	}{
		{
			"Basic Usage",
			map[string][]string{
				"APlayer": {"Player.h"},
				"FData":   {"Data.h"},
			},
			"#include \"Player.h\"\n#include \"Data.h\"\nvoid T() { APlayer* P; FData D; D.S=0; }",
			map[string]string{"Player.h": "Forward", "Data.h": "Essential"},
		},
		{
			"Redundant Include",
			map[string][]string{
				"AValid": {"Valid.h"},
				"AExtra": {"Extra.h"},
			},
			"#include \"Valid.h\"\n#include \"Extra.h\"\nvoid T() { AValid V; }",
			map[string]string{"Valid.h": "Essential", "Extra.h": "Redundant"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "audit_test")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpDir)

			cppPath := filepath.Join(tmpDir, "Test.cpp")
			if err := os.WriteFile(cppPath, []byte(test.cppContent), 0644); err != nil {
				t.Fatal(err)
			}

			summaries, err := analyzeCppFiles(tmpDir, IncludeRegex, test.registry)
			if err != nil {
				t.Fatal(err)
			}

			if len(summaries) != 1 {
				t.Fatalf("Expected 1 summary, got %d", len(summaries))
			}

			includeMap := make(map[string]string)
			for _, inc := range summaries[0].Includes {
				includeMap[inc.Name] = inc.Status
			}

			for incName, expectedStatus := range test.expectedStatuses {
				if includeMap[incName] != expectedStatus {
					t.Errorf("Include %s: got status %s, want %s", incName, includeMap[incName], expectedStatus)
				}
			}
		})
	}
}

// TestGenerateDashboard verifies that the HTML report is generated without errors for various scenarios.
func TestGenerateDashboard(t *testing.T) {
	tests := []struct {
		name      string
		summaries []FileSummary
	}{
		{
			"Standard Case",
			[]FileSummary{
				{
					Path: "Test.cpp",
					Includes: []IncludeStatus{
						{Name: "Player.h", Status: "Forward", SuggestedForward: "class APlayer;"},
						{Name: "Data.h", Status: "Essential"},
					},
					TotalIncludes:  2,
					ForwardCount:   1,
					RedundantCount: 0,
				},
			},
		},
		{
			"Empty Project",
			[]FileSummary{},
		},
	}

	// Ensure template exists
	templatePath := "template.html"
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		dummyTemplate := `<html><body>{{TOTAL_FILES}} {{TOTAL_REDUNDANT}} {{RATIO}} <!-- RESULTS_PLACEHOLDER --></body></html>`
		os.WriteFile(templatePath, []byte(dummyTemplate), 0644)
		defer os.Remove(templatePath)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := generateDashboard(test.summaries)
			if err != nil {
				t.Errorf("generateDashboard failed: %v", err)
			}
			if _, err := os.Stat("dashboard.html"); os.IsNotExist(err) {
				t.Error("dashboard.html was not created")
			}
			os.Remove("dashboard.html")
		})
	}
}

// TestFullUsageDetection verifies our "Essential vs Forward" logic.
// It ensures that static calls (::) correctly trigger an Essential status.
func TestFullUsageDetection(t *testing.T) {
	testSymbol := "Logger"
	// We use the exported GetFullUsageRegex function from IncludeAudit.go
	// Test without header context for simple patterns
	fullUsageRegex := GetFullUsageRegex(testSymbol, "")

	tests := []struct {
		name     string
		content  string
		expected bool // true = Essential, false = Forward candidate
	}{
		{"Static Call", "Logger::addMessage()", true},
		{"Template Argument", "Cast<Logger>(Other)", true},
		{"Pointer Check", "if (Logger* Ptr)", false}, // Forward Declaration is enough
		{"Member Access", "Logger.Field", true},
		{"Pointer Access", "Logger->Field", true},
		{"Construction", "new Logger()", true},
		{"Local Variable", "Logger Data; Data.Score = 0;", true},
		{"Constructor Call", "Logger Params()", true},               // Stack construction requires full header
		{"Substring (Should not match)", "MyLogger::Call()", false}, // Verifies word boundaries (\b)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := fullUsageRegex.MatchString(test.content)
			if got != test.expected {
				t.Errorf("fullUsageRegex match %q: got %v, want %v", test.content, got, test.expected)
			}
		})
	}

	t.Run("Variable Member Access from Header", func(t *testing.T) {
		header := "TObjectPtr<class Logger> MyComponent;"
		regex := GetFullUsageRegex(testSymbol, header)
		content := "MyComponent->isValid = true;"
		if !regex.MatchString(content) {
			t.Errorf("Expected match for variable member access defined in header")
		}
	})
}

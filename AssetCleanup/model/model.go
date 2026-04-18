// Package model defines the shared data types used across all GoPurge packages.
//
// All other packages (discovery, scanner, analyzer, reporter) import model to
// produce or consume these types. model itself imports nothing from GoPurge,
// which guarantees it can never introduce an import cycle.
package model

import "time"

// FileEntry represents a single discovered asset file on disk.
// Populated progressively through the pipeline: Path and Size are set by
// discovery, SHA256 is set only after the scanner hashes the file.
type FileEntry struct {
	// Path is the absolute path to the file.
	// On Windows, paths exceeding 260 characters are prefixed with \\?\ to
	// bypass the MAX_PATH limit.
	Path string

	// Size is the file size in bytes, read from the directory entry.
	Size int64

	// SHA256 is the hex-encoded SHA-256 digest of the full file contents.
	// Empty string until the scanner has processed this entry.
	SHA256 string

	// VerifyManually indicates that this entry may be a false positive —
	// typically a DataTable or config-driven asset that is loaded by string
	// path at runtime and therefore appears unreferenced in a static scan.
	VerifyManually bool
}

// FileGroup is a set of two or more files that are byte-for-byte identical,
// confirmed by matching SHA-256 digests. Only the scanner produces this type.
type FileGroup struct {
	// Hash is the shared SHA-256 digest of every file in the group.
	Hash string

	// Files contains at least two FileEntry values with the same Hash.
	Files []FileEntry
}


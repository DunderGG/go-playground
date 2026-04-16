# GoPurge 🧹

GoPurge is a high-performance, read-only CLI diagnostic tool written in Go for Unreal Engine developers. It scans your project's `Content/` and `Source/` directories to identify redundant assets, unreferenced files, and large "waste" that bloats Git LFS and slows down backups.

**GoPurge is strictly read-only.** It generates a comprehensive report but never modifies or deletes your files.

## Features

- **Multi-Stage Duplicate Detection:** Efficiently finds byte-for-byte identical assets by comparing sizes, then headers, and finally full SHA-256 hashes using a parallel worker pool.
- **Reference Analysis:** Scans `.uasset`/`.umap` binaries for Soft Object Paths and searches C++ source files for `FSoftObjectPath` to identify assets that are not being used in your project's dependency graph.
- **Large File Scanning:** Quickly flags assets exceeding a configurable size threshold.
- **OS Optimized:** Handles Windows `MAX_PATH` limits (>260 characters) using the `\\?\` prefix and safely skips symbolic links/junctions.
- **Low Memory Footprint:** Streams file contents during hashing to keep RAM usage under ~50MB regardless of project size.

## Installation

```bash
go build -o gopurge.exe .
```

## Usage

Run GoPurge from your terminal, pointing it to the root of your Unreal Engine project.

```bash
gopurge -project-dir="C:/Projects/MyUnrealProject"
```

### Command Line Options

| Flag | Description | Default |
| :--- | :--- | :--- |
| `-project-dir` | **(Required)** Path to the Unreal Engine project root. | |
| `-output` | Report format: `json` or `csv`. | `json` |
| `-workers` | Number of concurrent goroutines for hashing. | `4` |
| `-large-threshold` | Size in MB above which a file is flagged as "large". | `100` |
| `-report-path` | Custom output path for the report file. | `./gopurge_report.json` |

## How it Works

1. **Pre-flight Check:** Verifies a `.uproject` file exists and ensures the Unreal Editor is closed to avoid file locks.
2. **Discovery:** Recursively walks the `Content/` folder, skipping `Intermediate/`, `Saved/`, and `Binaries/`.
3. **Scan:**
   - **Duplicates:** Groups files by size → 1KB header → Full Hash.
   - **Large Files:** Simple size-based filter.
   - **References:** Parses binary assets for strings like `/Game/Path/To/Asset`.
4. **Report:** Assembles findings into a formatted report and prints a summary of "Total Reclaimable" space.

## Important Note

GoPurge identifies "potential" waste. Certain assets (like DataTables or assets loaded purely via dynamic string paths in C++) may appear unreferenced even if they are used. **Always review the generated report and verify assets in the Unreal Editor before manually deleting them.**

---

*Built with Go. Optimized for Unreal Engine.*

# <img width="64" height="64" alt="appicon" src="https://github.com/user-attachments/assets/0b314a1d-b14d-4a07-9c8b-af04b7eeca8c" /> &emsp; **ToDopher**

**ToDopher** is a lightning-fast source code auditor. It's designed to help engineering teams manage technical debt by extracting `TODO`, `FIXME`, and `BUG` comments from large codebases (like Unreal Engine projects) and presenting them in a beautiful, searchable dashboard.

###### ToDo + Gopher = ToDopher. Get it? It's a pun. You're welcome...

## 🚀 Features

- **Concurrent Scanning**: Uses a Goroutine worker pool to audit thousands of files in seconds.
- **Smart Filtering**: Automatically ignores common "noise" directories like `Intermediate/`, `Binaries/`, and `.git/`.
- **Regex Extraction**: Captures not just the comment, but the line number and the optional **author** (e.g., `TODO-Dunder: fix this`).
- **Interactive Report**: Generates a standalone, dark-mode-first HTML report powered by DataTables for instant filtering and sorting.
- **UE-Ready**: Pre-configured with filters for header files, source code, and configuration files common in Unreal Engine.

## 📦 Getting Started

### Prerequisites
- [Go 1.20+](https://go.dev/dl/)

### How to Run
The easiest way to run **ToDopher** is using the `go run` command:
```powershell
go run ToDopher.go "C:\Path\To\Your\UnrealProject"
```
If no path is provided, it defaults to scanning the current directory.

Alternatively, you can build a standalone executable:
```powershell
go build -o ToDopher.exe ToDopher.go
./ToDopher.exe "C:\Path\To\Your\UnrealProject"
```

## 📖 Usage

1. **Launch**: Open the GoVid application.
2. **URL**: Paste the video URL into the input field.
3. **Configuration**: Select your desired output format (MP4, MKV, or MP3) and maximum resolution.
4. **Save Location**: Choose the directory where you want to save the file.
5. **Download**: Click the "Download" button to start the process. You can monitor the progress in real-time.

### Command Line Options

- `--update`: Updates the underlying `yt-dlp` tool to the latest version.

## 📊 The Report
After running the audit, ToDopher generates a `report.html` file in the project folder. 

1. **Dark Mode First**: Optimized for long coding sessions.
2. **Instant Search**: Find all `FIXME` items or everything assigned to a specific author instantly.
3. **No Server Required**: The report is a portable, standalone file.

## 🛠️ Configuration
Currently, ToDopher is configured via the `Config` struct in [ToDopher.go](ToDopher.go#L39-L43):
- **Search Tags**: `TODO`, `FIXME`, `HACK`, `BUG`, `SUGGESTION`, `IDEA`, `REWORK`.
- **Allowed Extensions**: `.h`, `.cpp`, `.cs`, `.py`, `.ini`, `.go`, `.java`, `.html`.

## 📜 Roadmap
- [ ] **Git Blame Integration**: Automatically fetch the author and date of each TODO from Git history.
- [ ] **Context Lines**: Capture 2-3 lines of surrounding code for better auditing.
- [ ] **JSON/Markdown Export**: For integration with CI/CD pipelines.
- [ ] **Custom Config**: For more tags, extensions and ignore folders.
- [ ] **GUI**: For easier usage.

## 🛠️ Built With

- TODO

## 👤 Author

**David Bennehag** - [@dunder](https://github.com/dunder) - [dunder.gg](https://dunder.gg)

## 📄 License

This project is licensed under the GPL-3.0 - see the [LICENSE](LICENSE) file for details.

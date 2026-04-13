# Unreal Include Auditor (Go)

A high-performance static analysis tool written in Go to identify redundant `#include` statements and suggest forward declarations in Unreal Engine C++ projects. This tool helps significantly reduce compilation times by cleaning up header dependencies.

## 🚀 Features

- **Global Symbol Registry**: Scans your entire source directory to map classes, structs, and enums to their defining header files.
- **Context-Aware Analysis**: Analyzes `.cpp` files by simultaneously looking at their corresponding `.h` files to track variable types.
- **Unreal Engine Optimized**:
    - Supports `TObjectPtr<T>`, `TSubclassOf<T>`, and raw pointers.
    - Handles Unreal API macros (e.g., `MYPROJECT_API`).
    - Skips `.generated.h` files automatically.
    - Identifies `Cast<T>`, `NewObject<T>`, and other template-based usages.
- **Interactive Dashboard**: Generates a `dashboard.html` report with "File Health" scores and suggested forward declarations.
- **Dark Mode Support**: A developer-friendly UI that defaults to dark mode.

## 🛠️ How to Use

1. **Install Go**: Ensure you have [Go](https://go.dev/) installed on your machine.
2. **Clone/Setup**: Place `IncludeAudit.go` and `template.html` in a folder.
3. **Run the Audit**:
   Open a terminal and run:
   ```bash
   go run IncludeAudit.go "C:/Path/To/Your/UnrealProject/Source"
   ```
4. **View Results**: Open the generated `dashboard.html` in any web browser.

## 🧠 Solved Edge Cases

Throughout development, we have implemented sophisticated logic to handle complex C++ and Unreal-specific scenarios:

- **Private/Public Resolution**: Automatically resolves header paths for proper directory structures, where `.cpp` is in `Private/` and `.h` is in `Public/`.
- **Hidden Member Access**: Detects when a header is essential because a member is accessed via a variable defined in the header (e.g., `abilitySystemComponent->SetTag()` makes the include essential even if the class name `UPlayerAbilitySystemComponent` never appears in the `.cpp`).
- **Forward Declaration Collision**: Distinguishes between a class definition (`class FMyClass {`) and a forward declaration (`class FMyClass;`) to avoid registry corruption.
- **Comment Stripping**: Uses a multi-pass regex to ignore symbols mentioned inside `//` or `/* */` comments.
- **TObjectPtr & Templates**: Specifically handles the `TObjectPtr<class Symbol>` syntax often found in Unreal Engine 5 header files.

## ⏳ To-Do / Planned Features

- [ ] **Recursive Dependency Tracking**: Calculate the "Weight" of a header by seeing how many other headers it includes recursively.
- [ ] **Circular Dependency Detection**: Highlight headers that include each other, causing "Big Block" recompiles.
- [ ] **Automated Fixing**: Optional flag to automatically comment out redundant includes or insert suggested forward declarations.
- [ ] **Include-What-You-Use (IWYU) Integration**: Cross-reference with Unreal's IWYU tool for even deeper analysis.
- [ ] **Namespace Support**: Better handling of deep nested C++ namespaces outside of standard Unreal conventions.

---
*Created with ❤️ for Unreal Engine Developers.*

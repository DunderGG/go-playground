	
* DETAILED IMPLEMENTATION PLAN

    ** 1. HEURISTIC PARSING (FAST)
	    - Scan files for `#include "..."`.
	    - Map every project header to the symbols it defines (Classes, Structs, Enums).

	** 2. SYMBOL USAGE TRACKER
        - For file X, list all Symbols it uses.
        - Check if Symbol S is defined in any of the headers included by file X.
        - If Symbol S is only used as `class S*` or `struct S*`, it's a Forward Declare candidate.

	** 3. REPORTING
        - List "Unused Includes" per file.
        - List "Forward Declaration Candidates".
        - Rank files by "Include Weight" (impact on compile time).

	** 4. "IN-SITU" VERIFICATION
        - Optional: Temporarily comment out an include and run `UnrealEditor-Cmd.exe`
        to see if it still compiles (The "Brute Force" verification).

	**5. DEPTH ANALYZER
        - Calculate the "Recursive Weight" of an include (how many other headers it pulls in).
        - Flag headers that pull in `Engine.h` as "Critical Performance Risks".

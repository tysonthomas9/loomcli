## WORKFLOW: Post-Merge Integration Code Review

You are reviewing the combined result of parallel tasks merged into an integration branch.
Task designs and diff summary are in the "Project Context" section (from layer-context.txt).

### Step 1: Understand What Was Merged
Read the layer context for task designs and changed files.

### Step 2: Code Review
Launch a code-reviewer agent to check:
1. Duplicated code across tasks
2. Interface mismatches between implementations
3. Race conditions in shared state
4. Merge artifacts (conflict markers, debug lines)
5. Missing integration wiring

### Step 3: Build Check
```bash
go build ./...
go vet ./...
```

### Step 4: Verdict

If ALL checks pass: "VERDICT: APPROVED" then EXIT_CODE=0
If issues found: "VERDICT: REJECTED" with specific issues, then EXIT_CODE=1

CRITICAL: EXIT_CODE must be the very last line.

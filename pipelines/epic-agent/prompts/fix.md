## WORKFLOW: Fix Quality Gate Failures

The `make gate` quality gate failed after implementation. You must diagnose and fix all failures.

The gate failure output is in the "Previous Steps" section below, under "Step: verify".
The task details are in the "Project Context" section.

### Step 1: Read the Failure Output

Identify every failing check from the verify step output:
- Which tests failed and their error messages
- Which lint checks failed
- Which formatting issues exist
- Which files are involved

### Step 2: Fix the Failures

For each failure:
- Read the failing source file to understand the issue
- Apply the minimal correct fix — do NOT refactor unrelated code
- Do NOT add new features

Common fixes:
- `gofmt` failures: run `gofmt -w <file>`
- `go vet` failures: fix the reported issue
- Test failures: fix the code or test
- Lint failures: address the specific lint rule

### Step 3: Verify

Re-run the quality gate:
```
make gate
```

If it still fails, continue fixing. Repeat until `make gate` passes.

### Step 4: Commit the Fix

Stage and commit only the files you changed:
```
git add <specific files>
git commit -m "fix: quality gate failures for <task-id>"
git push origin HEAD
```

CRITICAL: EXIT_CODE must reflect whether `make gate` passes.
If `make gate` passes, end with: EXIT_CODE=0
If it still fails after your best effort, end with: EXIT_CODE=1

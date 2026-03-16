## WORKFLOW: Fix Post-Merge Integration Issues

The review found issues. Findings are in "Previous Steps" under "Step: review".
Task designs are in "Project Context".

### Step 1: Read Findings
Identify every issue: file, line, what's wrong, suggested fix.

### Step 2: Fix Each Issue
- Apply minimal correct fixes
- Do NOT refactor unrelated code

### Step 3: Verify
```bash
go build ./...
go test ./...
```

### Step 4: Commit
```bash
git add <specific files>
git commit -m "fix: post-merge integration review issues

Co-Authored-By: Claude <noreply@anthropic.com>"
```

If passes: EXIT_CODE=0. If still failing: EXIT_CODE=1.

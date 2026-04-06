## WORKFLOW: Resolve Merge Conflicts

You are resolving merge conflicts for: {{ .SourceBranch }} -> {{ .TargetBranch }}
{{ .SafetyBlock }}
### Conflicted Files
The following files have conflicts:
{{ .ConflictList }}

### Step 1: Understand the Conflict
For each conflicted file:
- Read the file to see the conflict markers (<<<<<<, =======, >>>>>>>)
- Understand what changes came from each branch
- The HEAD section is from {{ .TargetBranch }} (current branch)
- The incoming section is from {{ .SourceBranch }} (being merged)

### Step 2: Resolve Each Conflict
For each conflicted file:
- Determine the correct resolution (keep one side, combine both, or write new code)
- Edit the file to remove ALL conflict markers
- Ensure the resulting code is syntactically correct
- Ensure the logic makes sense with both sets of changes integrated

### Step 3: Verify Resolution
- Run any relevant build commands to ensure code compiles
- Run tests if available
- Check that no conflict markers remain: grep -r '<<<<<<' . or grep -r '>>>>>>>' .

### Step 4: Complete the Merge
Once all conflicts are resolved:
```bash
git add -A
git commit -m "Resolve merge conflicts: {{ .SourceBranch }} -> {{ .TargetBranch }}

Conflicts resolved in:
{{ .ConflictList }}

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"
git push origin {{ .PushRef }}
```

### Step 5: Verify
- Run 'git status' to confirm clean working tree
- Confirm push succeeded

### CRITICAL: Do Not Leave Conflicts
- Every conflict marker must be removed
- The code must compile/build
- If you cannot resolve a conflict, explain why and do NOT commit

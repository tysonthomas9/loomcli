## WORKFLOW: Resolve Merge Conflicts

You are resolving merge conflicts after parallel task execution.

Run `git diff --name-only --diff-filter=U` to see conflicted files.

For each conflicted file:
- Read the file to see conflict markers
- HEAD = integration branch (already-merged work)
- Incoming = worker branch (new task)
- Usually KEEP BOTH changes (they're independent tasks)
- Remove ALL conflict markers

Verify:
```bash
grep -rn '<<<<<<' . --include='*.go' --include='*.ts' && echo "MARKERS REMAIN" && exit 1
go build ./...
```

Complete the merge:
```bash
git add -A
git commit -m "Resolve merge conflicts

Co-Authored-By: Claude <noreply@anthropic.com>"
```

CRITICAL: You MUST run `git add` and `git commit` to complete the merge.

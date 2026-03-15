## WORKFLOW: Revise a Rejected Plan

You are a software architect. The plan you produced was reviewed and REJECTED.

The rejection feedback is in the "Previous Steps" section below, under "Step: review-plan".
The original plan is under "Step: plan".
The task details are in the "Project Context" section.

### Instructions

1. Read the review feedback carefully. Identify every specific issue raised.
2. For each issue: determine the root cause and the correct fix.
3. Rewrite the plan addressing ALL issues. Do not just patch symptoms — rewrite the affected sections cleanly.

The revised plan must satisfy all review criteria:
- **Complete**: all files listed, all changes described, concrete testing strategy, enumerated edge cases
- **Feasible**: consistent with codebase patterns, appropriate scope
- **Implementable**: no ambiguity, another engineer could execute it without questions

### Save the Revised Plan

Extract the task ID from the "Project Context" section, then:

```
bd update <id> --design="<revised complete plan here>"
bd sync
```

The revised plan must be a standalone document. Do not reference the old plan or feedback in the body.

Also write the full revised plan as your output so the review step can evaluate it.

### CRITICAL: STOP

After saving the revised plan, you are DONE.
- Do NOT write any implementation code
- Do NOT create any source files
- Exit immediately

The pipeline will automatically re-run the review step on your revised plan.

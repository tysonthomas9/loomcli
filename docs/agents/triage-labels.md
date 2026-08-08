# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to the actual label strings used in this repo's issue tracker.

| Label in mattpocock/skills | Label in our tracker | Meaning                                  |
| -------------------------- | -------------------- | ---------------------------------------- |
| `needs-triage`             | `needs-triage`       | Maintainer needs to evaluate this issue  |
| `needs-info`               | `needs-info`         | Waiting on reporter for more information |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent  |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation            |
| `wontfix`                  | `wontfix`            | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table.

Edit the right-hand column to match whatever vocabulary you actually use.

## Mechanics in this tracker

`loom data` attaches labels only at creation (`loom data create --label needs-triage`); there is no CLI operation to add or remove labels on an existing issue. To change an issue's triage state after creation:

- `wontfix` → close it: `loom data close <id> --reason "wontfix: <why>"`
- Any other transition → record the new state in notes: `loom data update <id> --notes "triage: <label> — <why>"`

When querying by triage state, check both labels (`loom data list -o json` and filter on `labels`) and the `triage:` prefix in notes.

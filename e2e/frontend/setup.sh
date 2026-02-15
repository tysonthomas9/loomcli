#!/usr/bin/env bash
# setup.sh — Create an isolated test environment for the frontend E2E test.
#
# Scaffolds a React+Vite+TypeScript project (cortex-kanban), initializes
# git+beads, creates worktrees, writes loom configs, and seeds beads with
# 3 epics and 8 tasks with full descriptions and inter-task dependencies.
#
# Usage: ./setup.sh [test_dir]
#   test_dir defaults to /tmp/loom-frontend-e2e

set -euo pipefail

TEST_DIR="${1:-/tmp/loom-frontend-e2e}"

if [ -d "$TEST_DIR" ]; then
  echo "!! $TEST_DIR already exists. Remove it first or choose another path."
  echo "   Run: rm -rf $TEST_DIR"
  exit 1
fi

echo "==> Creating test repo at $TEST_DIR"
mkdir -p "$TEST_DIR"
cd "$TEST_DIR"

# Initialize git repo
git init -q
git commit --allow-empty -q -m "initial commit"

# Initialize beads
bd init -q

# ============================================================
# Section 2: Project Scaffold (React + Vite + TypeScript)
# ============================================================

cat > package.json <<'EOF'
{
  "name": "cortex-kanban",
  "version": "1.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "react": "^18.3.1",
    "react-dom": "^18.3.1"
  },
  "devDependencies": {
    "@types/react": "^18.3.3",
    "@types/react-dom": "^18.3.0",
    "@vitejs/plugin-react": "^4.3.1",
    "typescript": "^5.5.3",
    "vite": "^5.4.0"
  }
}
EOF

cat > tsconfig.json <<'EOF'
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "isolatedModules": true,
    "moduleDetection": "force",
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,
    "noUncheckedSideEffectImports": true
  },
  "include": ["src"],
  "exclude": ["src/**/*.test.ts", "src/**/*.test.tsx", "src/test"]
}
EOF

cat > vite.config.ts <<'EOF'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
})
EOF

cat > index.html <<'EOF'
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Cortex Kanban</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
EOF

mkdir -p src/components src/data

cat > src/vite-env.d.ts <<'EOF'
/// <reference types="vite/client" />
EOF

cat > src/main.tsx <<'EOF'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './index.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
EOF

cat > src/index.css <<'EOF'
*, *::before, *::after {
  box-sizing: border-box;
}

body {
  margin: 0;
  padding: 0;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen,
    Ubuntu, Cantarell, 'Fira Sans', 'Droid Sans', 'Helvetica Neue', sans-serif;
  background: #f5f3ee;
  color: #1a1a1a;
}
EOF

cat > src/App.tsx <<'EOF'
export default function App() {
  return <div>Cortex</div>
}
EOF

# Placeholder CSS module for App (agent will replace with real styles)
cat > src/App.module.css <<'EOF'
/* Placeholder — replaced by agent implementing TASK_APP */
EOF

# ============================================================
# Section 3: Install Dependencies
# ============================================================

echo "==> Installing npm dependencies"
npm install --loglevel=error

# ============================================================
# Section 4: Commit Scaffold
# ============================================================

git add -A
git commit -q -m "Add React+Vite+TypeScript scaffold"

# ============================================================
# Section 5: Create Worktrees
# ============================================================

git worktree add -q ./worktrees/falcon -b falcon
git worktree add -q ./worktrees/nova -b nova

# ============================================================
# Section 6: Loom Configs
# ============================================================

cat > loom.yaml <<'EOF'
daemon:
  pid_file: .loom/daemon.pid
  log_dir: .loom/logs
  restart_policy:
    max_retries: 10
    backoff_initial: 2
    backoff_max: 30

agents:
  - worktree: falcon
    role: task
    auto: true
  - worktree: nova
    role: task
    auto: true
EOF

cat > loom-plan.yaml <<'EOF'
daemon:
  pid_file: .loom/daemon.pid
  log_dir: .loom/logs
  restart_policy:
    max_retries: 10
    backoff_initial: 2
    backoff_max: 30

agents:
  - worktree: falcon
    role: plan
    auto: true
  - worktree: nova
    role: plan
    auto: true
EOF

cat > loom-task.yaml <<'EOF'
daemon:
  pid_file: .loom/daemon.pid
  log_dir: .loom/logs
  restart_policy:
    max_retries: 10
    backoff_initial: 2
    backoff_max: 30

agents:
  - worktree: falcon
    role: task
    auto: true
  - worktree: nova
    role: task
    auto: true
EOF

mkdir -p .loom/logs

# ============================================================
# Section 7: Seed Beads (3 Epics, 8 Tasks)
# ============================================================

echo "==> Creating test epics and tasks"

# --- Epics ---

EPIC_1=$(bd create --title="Project Foundation & Layout" --type=epic --priority=1 \
  --json 2>/dev/null | jq -r '.id')
[ -n "$EPIC_1" ] || { echo "FAIL: Could not create Epic 1"; exit 1; }

EPIC_2=$(bd create --title="Kanban Board Components" --type=epic --priority=1 \
  --json 2>/dev/null | jq -r '.id')
[ -n "$EPIC_2" ] || { echo "FAIL: Could not create Epic 2"; exit 1; }

EPIC_3=$(bd create --title="Sidebar & Stats Panel" --type=epic --priority=2 \
  --json 2>/dev/null | jq -r '.id')
[ -n "$EPIC_3" ] || { echo "FAIL: Could not create Epic 3"; exit 1; }

# --- Task descriptions (defined as variables to avoid bash 3.2 heredoc nesting issues) ---

DESC_DATA='Create src/data/mockData.ts.

Export interfaces:
- Agent { id:string; name:string; role:string; status:"ready"|"working"|"error"|"idle"; avatar:string; color:string; commitCount:number; statusText:string; }
- Issue { id:string; title:string; priority:"P0"|"P1"|"P2"|"P3"; status:"backlog"|"open"|"blocked"|"in_progress"|"review"|"done"; assignedAgent?:string; agentActivity?:string; issueType:"task"|"bug"|"feature"; }
- WorkQueueStats { backlog:number; open:number; blocked:number; inProgress:number; needsReview:number; done:number; }

Export const agents: Agent[] with 6 agents:
- cobalt (Developer, working, #7c8aab, 366, "2 changes")
- dev1 (Developer, working, #e8955a, 381, "1 changes")
- ember (QA, error, #b589b0, 371, "Error")
- falcon (Developer, working, #7bab8a, 366, "2 changes")
- nova (Architecture, ready, #6b9dad, 366, "Ready")
- zephyr (Developer, ready, #7c8aab, 366, "Ready")

Export const issues: Issue[] with 20 issues distributed: 6 backlog, 3 open, 3 blocked, 3 in_progress (assigned to falcon/cobalt/dev1 with activity strings), 2 review (assigned to ember/nova with activity strings), 3 done. Use realistic task titles: "URGENT: Database migration failure in production" (P0, backlog), "Convert web UI to server-initiated push" (P2, backlog), etc.

Export const workQueueStats matching the distribution.
Export a helper function getAgentById(id:string):Agent|undefined.'

DESC_HEADER='Create src/components/Header/Header.tsx and src/components/Header/Header.module.css.

Props interface HeaderProps: onSearch(query:string)=>void, onFilterPriority(p:string|null)=>void, onFilterType(t:string|null)=>void.

Layout: flex row, align-items center, justify-content space-between, height 56px, padding 0 20px, background white, border-bottom 1px solid #e0dcd4.

Left: h1 "Cortex" (font-size 20px, font-weight 700, color #1a1a1a).
Center: input type="text" placeholder="Search tasks..." (background #f5f3ee, border 1px solid #d5d0c8, border-radius 6px, padding 8px 12px, width 300px, font-size 14px).
Right: two select dropdowns -- Priority (options: All priorities, P0, P1, P2, P3, P4) and Type (options: All types, task, bug, feature, epic). Selects styled: background white, border 1px solid #d5d0c8, border-radius 6px, padding 6px 10px, font-size 13px.

Export default function Header.'

DESC_APP='Create src/App.tsx (replace the scaffold placeholder) and src/App.module.css.

Import and render Header, AgentList, WorkQueue, and KanbanBoard components. Import mock data from ./data/mockData (agents, issues, workQueueStats).

Manage filter state with useState hooks (searchQuery string, priorityFilter string|null, typeFilter string|null). Filter the issues array by matching search query against title, priority against priorityFilter, and issueType against typeFilter before passing to KanbanBoard.

Layout: outer div with display:flex, flex-direction:column, height:100vh. Header full width at top. Below that a flex row: sidebar div (width 280px, padding 16px, display:flex, flex-direction:column, gap:16px, background:#EAE8E1, border-right:1px solid #d5d0c8) containing AgentList and WorkQueue, and main area (flex:1, overflow:hidden) containing KanbanBoard.

Export default function App.'

DESC_BOARD='Create src/components/KanbanBoard/KanbanBoard.tsx and src/components/KanbanBoard/KanbanBoard.module.css.

Props: KanbanBoardProps { issues:Issue[]; agents:Agent[]; }. Import KanbanColumn. Define column config array: [{key:"backlog",title:"Backlog"},{key:"open",title:"Open"},{key:"blocked",title:"Blocked"},{key:"in_progress",title:"In Progress"},{key:"review",title:"Needs Review"},{key:"done",title:"Done"}]. Filter issues by status for each column. Render KanbanColumn for each.

CSS: .board { display:flex; gap:12px; padding:20px; overflow-x:auto; height:100%; align-items:flex-start; }.

Export default function KanbanBoard.'

DESC_COLUMN='Create src/components/KanbanColumn/KanbanColumn.tsx and src/components/KanbanColumn/KanbanColumn.module.css.

Props: KanbanColumnProps { title:string; issues:Issue[]; agents:Agent[]; }. Import IssueCard and getAgentById from mockData. Render column header (flex row, title + count badge) and scrollable card list. For each issue, look up agent via getAgentById(issue.assignedAgent).

CSS:
.column { min-width:240px; max-width:240px; background:#EAE8E1; border-radius:12px; display:flex; flex-direction:column; max-height:100%; }
.header { padding:12px 16px; display:flex; justify-content:space-between; align-items:center; }
.title { font-size:13px; font-weight:600; color:#6b6560; text-transform:uppercase; letter-spacing:0.5px; }
.count { background:#d5d0c8; color:#6b6560; font-size:12px; font-weight:600; padding:2px 8px; border-radius:10px; }
.cardList { padding:0 8px 8px; display:flex; flex-direction:column; gap:8px; overflow-y:auto; }

Backlog column background should be slightly darker: #E4E2DB.

Export default function KanbanColumn.'

DESC_CARD='Create src/components/IssueCard/IssueCard.tsx and src/components/IssueCard/IssueCard.module.css.

Props: IssueCardProps { issue:Issue; agent?:Agent; }. Card displays: issue ID (monospace, color #999, font-size 11px) and priority badge (top row, flex justify-content space-between), title (font-size 14px, font-weight 500, color #1a1a1a, margin-top 6px).

Priority badge colors: P0=#e24b3b bg with white text, P1=#ef7f4a bg white text, P2=#f0b24a bg white text, P3=#5b85f7 bg white text, P4=#6b7280 bg white text. Badge: font-size 11px, font-weight 600, padding 2px 8px, border-radius 4px.

Conditional agent section (margin-top 10px): if issue.status==="in_progress" and agent exists, show agent avatar (20px circle with agent.color bg, white letter, font-size 10px) + agent name (font-size 12px, color #6b6560) + activity text (font-size 11px, color #999, italic). If issue.status==="review" and agent exists, show same agent info plus two buttons: "APPROVE" (background #2ea043, color white, font-size 11px, font-weight 600, padding 4px 12px, border-radius 4px, border none, cursor pointer) and "REJECT" (background #e24b3b, same styling). Buttons in flex row with gap 8px.

Card CSS: .card { background:white; border-radius:10px; padding:12px 14px; box-shadow:0 1px 3px rgba(0,0,0,0.06); cursor:pointer; }. .card:hover { box-shadow:0 2px 8px rgba(0,0,0,0.1); }.

Export default function IssueCard.'

DESC_AGENTLIST='Create src/components/AgentList/AgentList.tsx, src/components/AgentList/AgentList.module.css, src/components/AgentCard/AgentCard.tsx, and src/components/AgentCard/AgentCard.module.css.

AgentList props: AgentListProps { agents:Agent[]; }. Render header "Agents" with count badge, then list of AgentCard components.

AgentCard props: AgentCardProps { agent:Agent; }. Card layout: flex row, gap 10px, padding 10px 0, border-bottom 1px solid #d5d0c8. Avatar: 36px circle with agent.color bg, white centered letter (font-size 14px, font-weight 600). Info section: name (font-size 14px, font-weight 600, color #1a1a1a), role below (font-size 12px, color #8a857e). Right side: commit count in green (+N format, font-size 13px, font-weight 600, color #2ea043), status text below (font-size 12px, color based on status -- error=#e24b3b, ready=#8a857e, working/changes=#8a857e). Status dot: 8px circle positioned on bottom-right of avatar, colors: ready=#8a857e, working=#f0b24a, error=#e24b3b, idle=#8a857e.

Export default function AgentList and export default function AgentCard from their respective files.'

DESC_WORKQUEUE='Create src/components/WorkQueue/WorkQueue.tsx and src/components/WorkQueue/WorkQueue.module.css.

Props: WorkQueueProps { stats:WorkQueueStats; }. Render collapsible "Work Queue" section (use a useState for collapsed/expanded, default expanded).

Grid layout: display grid, grid-template-columns repeat(2, 1fr), gap 4px 16px. Each cell: status label (font-size 13px, color #6b6560) + count (font-size 13px, font-weight 600, color #1a1a1a, text-align right). Status labels: Backlog, Open, Blocked, In Progress, Needs Review, Done -- map to stats fields.

Below grid: summary line -- calculate total = sum of all stats, closed = stats.done, openCount = total - closed, percentage = Math.round(closed/total*100). Render: "{openCount} open - {closed} closed - {percentage}%" in font-size 13px color #6b6560. Chevron toggle for collapse.

CSS: .workQueue { border-top:1px solid #d5d0c8; padding-top:16px; }. .header { display:flex; justify-content:space-between; cursor:pointer; }.

Export default function WorkQueue.'

# --- Tasks under Epic 1: Project Foundation & Layout ---

TASK_DATA=$(bd create --title="Create mock data module with TypeScript types" \
  --type=task --priority=1 --parent="$EPIC_1" \
  --description="$DESC_DATA" --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_DATA" ] || { echo "FAIL: Could not create TASK_DATA"; exit 1; }

TASK_HEADER=$(bd create --title="Implement Header component with search and filter dropdowns" \
  --type=task --priority=2 --parent="$EPIC_1" \
  --description="$DESC_HEADER" --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_HEADER" ] || { echo "FAIL: Could not create TASK_HEADER"; exit 1; }

TASK_APP=$(bd create --title="Create App shell component with layout and filter state" \
  --type=task --priority=2 --parent="$EPIC_1" \
  --description="$DESC_APP" --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_APP" ] || { echo "FAIL: Could not create TASK_APP"; exit 1; }

# --- Tasks under Epic 2: Kanban Board Components ---

TASK_BOARD=$(bd create --title="Create KanbanBoard container with horizontal scroll layout" \
  --type=task --priority=1 --parent="$EPIC_2" \
  --description="$DESC_BOARD" --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_BOARD" ] || { echo "FAIL: Could not create TASK_BOARD"; exit 1; }

TASK_COLUMN=$(bd create --title="Implement KanbanColumn with header and card list" \
  --type=task --priority=2 --parent="$EPIC_2" \
  --description="$DESC_COLUMN" --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_COLUMN" ] || { echo "FAIL: Could not create TASK_COLUMN"; exit 1; }

TASK_CARD=$(bd create --title="Create IssueCard with priority badges and agent info" \
  --type=task --priority=2 --parent="$EPIC_2" \
  --description="$DESC_CARD" --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_CARD" ] || { echo "FAIL: Could not create TASK_CARD"; exit 1; }

# --- Tasks under Epic 3: Sidebar & Stats Panel ---

TASK_AGENTLIST=$(bd create --title="Implement AgentList sidebar with agent cards" \
  --type=task --priority=2 --parent="$EPIC_3" \
  --description="$DESC_AGENTLIST" --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_AGENTLIST" ] || { echo "FAIL: Could not create TASK_AGENTLIST"; exit 1; }

TASK_WORKQUEUE=$(bd create --title="Create WorkQueue stats panel" \
  --type=task --priority=2 --parent="$EPIC_3" \
  --description="$DESC_WORKQUEUE" --json 2>/dev/null | jq -r '.id')
[ -n "$TASK_WORKQUEUE" ] || { echo "FAIL: Could not create TASK_WORKQUEUE"; exit 1; }

# ============================================================
# Section 8: Inter-Task Dependencies
# ============================================================

# NOTE: Dependencies are NOT added here. The daemon uses `bd ready` which respects
# dependencies, blocking both planning AND implementation. Adding deps in setup
# serializes the planning phase unnecessarily.
#
# Instead, deps are added by test-frontend-e2e.sh between Phase 2 (review) and
# Phase 3 (implementation) so that:
#   - Phase 1 (planning): all 8 tasks plannable in parallel
#   - Phase 3 (implementation): TASK_APP waits for all others (it imports everything)
echo "==> Skipping dependencies (added by test script before implementation)"

# ============================================================
# Section 9: Env File
# ============================================================

ENV_FILE="$TEST_DIR/test-env.sh"
cat > "$ENV_FILE" <<EOF
export TEST_DIR="$TEST_DIR"
export EPIC_1="$EPIC_1"
export EPIC_2="$EPIC_2"
export EPIC_3="$EPIC_3"
export TASK_DATA="$TASK_DATA"
export TASK_HEADER="$TASK_HEADER"
export TASK_APP="$TASK_APP"
export TASK_BOARD="$TASK_BOARD"
export TASK_COLUMN="$TASK_COLUMN"
export TASK_CARD="$TASK_CARD"
export TASK_AGENTLIST="$TASK_AGENTLIST"
export TASK_WORKQUEUE="$TASK_WORKQUEUE"
EOF

# ============================================================
# Section 10: Sync & Summary
# ============================================================

bd sync 2>/dev/null || true

echo ""
echo "==> Test environment ready at $TEST_DIR"
echo ""
echo "  Epics:"
echo "    EPIC_1=$EPIC_1  (Project Foundation & Layout)"
echo "    EPIC_2=$EPIC_2  (Kanban Board Components)"
echo "    EPIC_3=$EPIC_3  (Sidebar & Stats Panel)"
echo ""
echo "  Tasks (Epic 1 - Foundation):"
echo "    TASK_DATA=$TASK_DATA"
echo "    TASK_HEADER=$TASK_HEADER"
echo "    TASK_APP=$TASK_APP"
echo ""
echo "  Tasks (Epic 2 - Kanban):"
echo "    TASK_BOARD=$TASK_BOARD"
echo "    TASK_COLUMN=$TASK_COLUMN"
echo "    TASK_CARD=$TASK_CARD"
echo ""
echo "  Tasks (Epic 3 - Sidebar):"
echo "    TASK_AGENTLIST=$TASK_AGENTLIST"
echo "    TASK_WORKQUEUE=$TASK_WORKQUEUE"
echo ""
echo "  Worktrees: falcon, nova"
echo ""
echo "  Source the env file for individual tests:"
echo "    source $ENV_FILE"
echo ""
echo "  Verify with: cd $TEST_DIR && loom daemon --dry-run"

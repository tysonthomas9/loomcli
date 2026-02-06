# Chrome Visual Verification Tests for Server Push

Manual verification tests for the SSE-based real-time update system using Chrome browser automation tools.

## Prerequisites

Before running tests, ensure:

- [ ] Beads daemon is running: `bd daemon status` (if not, run `bd daemon start`)
- [ ] Web UI server is running on port 8080: `go run . -port 8080` (or use `air`)
- [ ] Chrome MCP extension is connected
- [ ] Test issues exist (or will be created during testing)

## Quick Start

```bash
# Terminal 1: Start daemon
bd daemon start

# Terminal 2: Start web UI
cd examples/beads-web-ui
go run . -port 8080
```

---

## Test Scenario 1: Connection Indicator States

**Purpose**: Verify the ConnectionStatus component correctly reflects daemon connection state.

### Test Steps

1. **Verify Connected State**
   - Load the web UI at `http://localhost:8080`
   - Look for the connection indicator in the UI
   - Verify the indicator shows green with "Connected" text
   - Use `read_page` to verify: `data-state="connected"` attribute

2. **Verify Disconnected State**
   - Stop the daemon: `bd daemon stop`
   - Observe the UI transition
   - Verify indicator shows red with "Disconnected" text
   - Use `read_page` to verify: `data-state="disconnected"` attribute

3. **Verify Reconnecting State**
   - Restart the daemon: `bd daemon start`
   - Watch for "Reconnecting (attempt N)..." text
   - Verify indicator shows amber/yellow during reconnection
   - Verify "Retry Now" button appears after first attempt
   - Use `read_page` to verify: `data-state="reconnecting"` attribute

4. **Verify Recovery**
   - Wait for automatic reconnection
   - Verify indicator returns to green "Connected" state
   - Verify no stale "Disconnected" state persists

### Expected DOM Attributes

```html
<!-- Connected -->
<div data-state="connected" role="status" aria-live="polite">
  <span class="indicator"></span>
  <span class="text">Connected</span>
</div>

<!-- Disconnected -->
<div data-state="disconnected" role="status" aria-live="polite">
  <span class="indicator"></span>
  <span class="text">Disconnected</span>
</div>

<!-- Reconnecting -->
<div data-state="reconnecting" role="status" aria-live="polite">
  <span class="indicator"></span>
  <span class="text">Reconnecting (attempt 1)...</span>
  <button aria-label="Retry connection now">Retry Now</button>
</div>
```

---

## Test Scenario 2: Real-Time Card Updates

**Purpose**: Verify issue cards update in real-time when changed externally via CLI.

### Test Steps

1. **Setup**
   - Note an existing issue ID from the UI, or create one:
     ```bash
     bd create --title="Visual Test Issue" --type=task --priority=2
     ```
   - Note the issue ID (e.g., `bd-xxxx`)

2. **Update Title**
   - Run in terminal: `bd update <id> --title="Updated Title Via CLI"`
   - Observe the UI immediately
   - Verify the card title updates within 2-3 seconds
   - Verify no page refresh or flicker occurs

3. **Update Priority**
   - Run: `bd update <id> --priority=0`
   - Verify priority badge changes from P2 to P0
   - Verify card appearance updates (P0 has different styling)

4. **Screenshot Comparison**
   - Take screenshot before update
   - Make change via CLI
   - Take screenshot after update
   - Compare to verify only the expected elements changed

### Verification

```bash
# Commands to test updates
bd update beads-xxx --title="New Title"      # Title should update instantly
bd update beads-xxx --priority=1              # Priority badge should change
bd update beads-xxx --description="New desc"  # (Only visible in detail panel)
```

---

## Test Scenario 3: No Visible Polling

**Purpose**: Verify the UI uses SSE push instead of polling.

### Test Steps

1. **Monitor Network Activity**
   - Open Chrome DevTools Network tab (or use `read_network_requests`)
   - Filter for requests to `/api/ready`
   - Tip: SSE connections show with type "eventsource" in the Type column
   - Load the page

2. **Observe Initial Load**
   - Verify ONE initial fetch to `/api/ready` (type: fetch/xhr)
   - Verify an SSE connection to `/api/events` (type: eventsource, stays open)

3. **Wait and Observe**
   - Watch the network tab for 30+ seconds
   - Verify NO periodic polling to `/api/ready`
   - Verify the SSE connection stays open (single long-lived connection)

4. **Verify No Flicker**
   - Watch the issue cards for 30+ seconds
   - Verify no periodic flicker or reload behavior
   - Cards should remain static unless externally changed

### Expected Network Pattern

- Initial: `GET /api/ready` (once)
- Ongoing: `GET /api/events` (SSE connection, stays open)
- NO repeated `/api/ready` calls

---

## Test Scenario 4: Smooth Transitions

**Purpose**: Verify status changes cause smooth card transitions between columns.

### Test Steps

1. **Setup**
   - Identify an issue in "Open" status
   - Note which column it's in

2. **Change Status**
   - Run: `bd update <id> --status=in_progress`
   - Watch the card carefully

3. **Verify Smooth Transition**
   - Card should move from "Open" column to "In Progress" column
   - Verify no jarring jumps or flash of incorrect state
   - Verify card maintains its appearance during move

4. **Test Multiple Transitions**
   ```bash
   bd update beads-xxx --status=in_progress  # Open -> In Progress
   bd update beads-xxx --status=review       # In Progress -> Review
   bd update beads-xxx --status=blocked      # Review -> Blocked
   bd update beads-xxx --status=open         # Blocked -> Open
   ```

### DOM Verification

```html
<!-- Card in column -->
<section data-status="open">
  <article data-priority="2">...</article>
</section>

<!-- After status change, card moves to new column -->
<section data-status="in_progress">
  <article data-priority="2">...</article>
</section>
```

---

## Test Scenario 5: Reconnection Feedback

**Purpose**: Verify proper feedback during connection loss and recovery.

### Test Steps

1. **Abrupt Disconnect**
   - Kill daemon abruptly: `pkill -9 -f "bd daemon"` or close terminal
   - Observe UI immediately

2. **Verify Reconnecting State**
   - ConnectionStatus should show "Reconnecting (attempt 1)..."
   - Attempt counter should increment over time
   - "Retry Now" button should appear after first attempt

3. **Test Retry Button**
   - Click "Retry Now" button
   - Verify immediate retry attempt (attempt counter resets or increments)

4. **Recover Connection**
   - Restart daemon: `bd daemon start`
   - Verify successful reconnection
   - Verify ConnectionStatus returns to "Connected"

5. **Verify Catch-Up**
   - Make a mutation while disconnected:
     ```bash
     bd update beads-xxx --title="Changed While Offline"
     ```
   - After reconnection, verify the UI shows the updated title
   - This confirms catch-up logic works

### Timing Expectations

- Disconnect detection: immediate when connection closes (browser EventSource detects this)
- First reconnect attempt: ~1 second after disconnect
- Reconnect interval: exponential backoff (1s, 2s, 4s, 8s... up to 30s max)
- Browser retry: EventSource auto-retries ~3 seconds apart (browser-native behavior)

---

## Manual Verification Commands

```bash
# Create test issue
bd create --title="Visual Test Issue" --type=task --priority=2

# Update title (should appear instantly in UI)
bd update beads-xxx --title="Updated Title"

# Change status (card should move columns)
bd update beads-xxx --status=in_progress

# Change priority (badge should update)
bd update beads-xxx --priority=0

# Stop daemon (UI should show disconnected)
bd daemon stop

# Start daemon (UI should reconnect)
bd daemon start

# Kill daemon abruptly (test reconnection)
pkill -9 -f "bd daemon"
```

---

## Verification Checklist

### Connection States
- [ ] Connected: Green indicator, "Connected" text
- [ ] Disconnected: Red indicator, "Disconnected" text
- [ ] Reconnecting: Amber indicator, "Reconnecting (attempt N)..." text
- [ ] Retry button appears only during reconnecting state
- [ ] Pulse animation visible during connecting/reconnecting (if not reduced-motion)

### Real-Time Updates
- [ ] Card titles update within 2-3 seconds of CLI change
- [ ] Priority badges update correctly
- [ ] No page reload required
- [ ] No visible flicker during updates

### Network Behavior
- [ ] Only one initial `/api/ready` fetch
- [ ] SSE connection to `/api/events` stays open
- [ ] No periodic polling visible

### Column Transitions
- [ ] Cards move smoothly between columns on status change
- [ ] No flash of incorrect column
- [ ] Card styling preserved during transition

### Reconnection
- [ ] Reconnection attempts shown with counter
- [ ] "Retry Now" button functional
- [ ] Catch-up works after reconnection
- [ ] Multiple concurrent updates handled correctly

---

## Troubleshooting

### Connection indicator stuck on "Connecting"
- Check daemon is running: `bd daemon status`
- Check web server logs for SSE errors
- Verify SSE endpoint: `curl -N http://localhost:8080/api/events`

### Updates not appearing
- Verify daemon is connected (check indicator)
- Check browser console for errors
- Verify SSE connection in Network tab

### Reconnection not working
- Check for browser console errors
- Verify daemon is accepting connections
- Try refreshing the page

---

## GIF Recording (Optional)

For documentation or debugging, use the GIF recorder:

```
1. Start recording: gif_creator action=start_recording
2. Perform test scenario
3. Stop recording: gif_creator action=stop_recording
4. Export: gif_creator action=export download=true filename="scenario-name.gif"
```

This captures multi-step interactions for review.

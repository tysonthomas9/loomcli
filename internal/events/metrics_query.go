package events

import "time"

// MetricsSnapshot is a point-in-time computed view of all metrics.
// Value type — safe to copy and serialize.
type MetricsSnapshot struct {
	Timestamp            time.Time          `json:"timestamp"`
	TasksCompletedLastHr int                `json:"tasks_completed_last_hour"`
	TasksCompleted24h    int                `json:"tasks_completed_24h"`
	AvgTaskDurationSec   float64            `json:"avg_task_duration_sec"`
	LinesChangedLastHr   int                `json:"lines_changed_last_hour"`
	ErrorRatePct         float64            `json:"error_rate_pct"`
	RestartCount24h      int                `json:"restart_count_24h"`
	RestartsByAgent      map[string]int     `json:"restarts_by_agent"`
	AgentUtilization     map[string]float64 `json:"agent_utilization"`
	TasksByRole          map[string]int     `json:"tasks_by_role"`
	TasksByEpic          map[string]int     `json:"tasks_by_epic"`
	TasksByAgent         map[string]int     `json:"tasks_by_agent"`
	HourlyCompletions    []HourlyBucket     `json:"hourly_completions"`

	TotalTasksCompleted int64 `json:"total_tasks_completed"`
	TotalTasksFailed    int64 `json:"total_tasks_failed"`
	TotalRestarts       int64 `json:"total_restarts"`
}

// HourlyBucket aggregates task metrics for a one-hour window.
type HourlyBucket struct {
	Hour        time.Time `json:"hour"`
	Completed   int       `json:"completed"`
	Failed      int       `json:"failed"`
	AvgDuration float64   `json:"avg_duration"`
}

// snapshotState holds data copied from MetricsStore under lock.
type snapshotState struct {
	now            time.Time
	tasks          []TaskMetric
	agents         []AgentMetric
	totalCompleted int64
	totalFailed    int64
	totalRestarts  int64
	utilState      map[string]agentUtilState
	retention      time.Duration
}

// Snapshot prunes stale data and computes a MetricsSnapshot.
func (ms *MetricsStore) Snapshot() MetricsSnapshot {
	state := ms.copyState()
	snap := newEmptySnapshot(state)
	aggregateTasks(state, &snap)
	aggregateAgentRestarts(state.agents, &snap)
	computeUtilization(state, &snap)
	return snap
}

func (ms *MetricsStore) copyState() snapshotState {
	ms.mu.Lock()
	ms.pruneLocked()

	state := snapshotState{
		now:            ms.now(),
		tasks:          make([]TaskMetric, len(ms.tasks)),
		agents:         make([]AgentMetric, len(ms.agents)),
		totalCompleted: ms.totalTasksCompleted,
		totalFailed:    ms.totalTasksFailed,
		totalRestarts:  ms.totalRestarts,
		utilState:      make(map[string]agentUtilState, len(ms.utilization)),
		retention:      ms.retention,
	}
	copy(state.tasks, ms.tasks)
	copy(state.agents, ms.agents)
	for k, v := range ms.utilization {
		state.utilState[k] = *v
	}

	ms.mu.Unlock()
	return state
}

func newEmptySnapshot(state snapshotState) MetricsSnapshot {
	return MetricsSnapshot{
		Timestamp:           state.now,
		TotalTasksCompleted: state.totalCompleted,
		TotalTasksFailed:    state.totalFailed,
		TotalRestarts:       state.totalRestarts,
		RestartsByAgent:     make(map[string]int),
		AgentUtilization:    make(map[string]float64),
		TasksByRole:         make(map[string]int),
		TasksByEpic:         make(map[string]int),
		TasksByAgent:        make(map[string]int),
	}
}

type bucketAccum struct {
	completed   int
	failed      int
	totalDurSec float64
	durationCnt int
}

func aggregateTasks(state snapshotState, snap *MetricsSnapshot) {
	oneHourAgo := state.now.Add(-time.Hour)
	var totalDuration time.Duration
	var durationCount int
	completed24h, failed24h := 0, 0
	hourlyMap := make(map[time.Time]*bucketAccum)

	for _, t := range state.tasks {
		b := getOrCreateBucket(hourlyMap, t.Timestamp.Truncate(time.Hour))
		if t.Success {
			completed24h++
			b.completed++
			if t.Duration > 0 {
				totalDuration += t.Duration
				durationCount++
				b.totalDurSec += t.Duration.Seconds()
				b.durationCnt++
			}
			if !t.Timestamp.Before(oneHourAgo) {
				snap.TasksCompletedLastHr++
				snap.LinesChangedLastHr += t.LinesAdded + t.LinesRemoved
			}
		} else {
			failed24h++
			b.failed++
		}
		if t.Role != "" {
			snap.TasksByRole[t.Role]++
		}
		if t.EpicID != "" {
			snap.TasksByEpic[t.EpicID]++
		}
		if t.Agent != "" {
			snap.TasksByAgent[t.Agent]++
		}
	}

	snap.TasksCompleted24h = completed24h
	if total := completed24h + failed24h; total > 0 {
		snap.ErrorRatePct = float64(failed24h) / float64(total) * 100
	}
	if durationCount > 0 {
		snap.AvgTaskDurationSec = totalDuration.Seconds() / float64(durationCount)
	}

	snap.HourlyCompletions = buildHourlyBuckets(hourlyMap)
}

func getOrCreateBucket(m map[time.Time]*bucketAccum, hour time.Time) *bucketAccum {
	b, ok := m[hour]
	if !ok {
		b = &bucketAccum{}
		m[hour] = b
	}
	return b
}

func buildHourlyBuckets(hourlyMap map[time.Time]*bucketAccum) []HourlyBucket {
	buckets := make([]HourlyBucket, 0, len(hourlyMap))
	for hour, b := range hourlyMap {
		bucket := HourlyBucket{
			Hour:      hour,
			Completed: b.completed,
			Failed:    b.failed,
		}
		if b.durationCnt > 0 {
			bucket.AvgDuration = b.totalDurSec / float64(b.durationCnt)
		}
		buckets = append(buckets, bucket)
	}
	sortHourlyBuckets(buckets)
	return buckets
}

func aggregateAgentRestarts(agents []AgentMetric, snap *MetricsSnapshot) {
	for _, a := range agents {
		if a.EventType == AgentRestarted {
			snap.RestartCount24h++
			if a.Agent != "" {
				snap.RestartsByAgent[a.Agent]++
			}
		}
	}
}

func computeUtilization(state snapshotState, snap *MetricsSnapshot) {
	cutoff := state.now.Add(-state.retention)
	for agent, s := range state.utilState {
		work := s.workDuration
		if s.isWorking && !s.lastStart.IsZero() {
			// Clamp lastStart to retention cutoff to avoid counting
			// work from before the window.
			effectiveStart := s.lastStart
			if effectiveStart.Before(cutoff) {
				effectiveStart = cutoff
			}
			work += state.now.Sub(effectiveStart)
		}
		// Clamp accumulated workDuration to retention window
		if work > state.retention {
			work = state.retention
		}
		if work > 0 {
			util := work.Seconds() / state.retention.Seconds()
			snap.AgentUtilization[agent] = util
		}
	}
}

func sortHourlyBuckets(buckets []HourlyBucket) {
	for i := 1; i < len(buckets); i++ {
		for j := i; j > 0 && buckets[j].Hour.Before(buckets[j-1].Hour); j-- {
			buckets[j], buckets[j-1] = buckets[j-1], buckets[j]
		}
	}
}

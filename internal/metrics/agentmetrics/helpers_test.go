package agentmetrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/tysonthomas9/loomcli/internal/metrics/spawnmetrics"
)

// writeSnapshot writes a spawn snapshot into runtimeDir at exactly the path the
// collector reads, using the production path helper.
func writeSnapshot(t *testing.T, runtimeDir string, snap spawnmetrics.Snapshot) {
	t.Helper()
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(spawnmetrics.SnapshotPath(runtimeDir), append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
}

// appendIndex appends JSON lines to <runtimeDir>/sessions/index.jsonl, the way
// sessions.Store.appendIndex does.
func appendIndex(t *testing.T, runtimeDir string, recs ...sessionRecord) {
	t.Helper()
	var buf []byte
	for _, rec := range recs {
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	appendRaw(t, runtimeDir, string(buf))
}

// appendRaw appends arbitrary bytes to the index, so a test can write a
// deliberately partial line.
func appendRaw(t *testing.T, runtimeDir, s string) {
	t.Helper()
	dir := filepath.Join(runtimeDir, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "index.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(s); err != nil {
		t.Fatalf("write index: %v", err)
	}
}

func finished(id, agent, phase, status string, duration float64) sessionRecord {
	ended := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	return sessionRecord{
		SessionID: id,
		AgentName: agent,
		Phase:     phase,
		Status:    status,
		DurationS: duration,
		EndedAt:   &ended,
	}
}

func running(id, agent, phase string) sessionRecord {
	return sessionRecord{SessionID: id, AgentName: agent, Phase: phase, Status: "running"}
}

// gather collects c through a private registry and returns the families by name.
func gather(t *testing.T, c prometheus.Collector) map[string]*dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := make(map[string]*dto.MetricFamily, len(families))
	for _, fam := range families {
		out[fam.GetName()] = fam
	}
	return out
}

// counterValue returns the value of the sample in family carrying exactly the
// given labels, and whether such a sample exists.
func counterValue(fam *dto.MetricFamily, labels map[string]string) (float64, bool) {
	if fam == nil {
		return 0, false
	}
	for _, m := range fam.GetMetric() {
		if !labelsMatch(m, labels) {
			continue
		}
		if m.GetCounter() != nil {
			return m.GetCounter().GetValue(), true
		}
		if m.GetGauge() != nil {
			return m.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

func histogramFor(fam *dto.MetricFamily, labels map[string]string) *dto.Histogram {
	if fam == nil {
		return nil
	}
	for _, m := range fam.GetMetric() {
		if labelsMatch(m, labels) {
			return m.GetHistogram()
		}
	}
	return nil
}

func labelsMatch(m *dto.Metric, want map[string]string) bool {
	got := make(map[string]string, len(m.GetLabel()))
	for _, l := range m.GetLabel() {
		got[l.GetName()] = l.GetValue()
	}
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

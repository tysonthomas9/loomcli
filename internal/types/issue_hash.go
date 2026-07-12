package types

import (
	"crypto/sha256"
	"fmt"
	"hash"
	"sort"
	"time"
)

// ComputeContentHash creates a deterministic hash of the issue's content.
// Uses all substantive fields (excluding ID, timestamps, and compaction metadata)
// to ensure that identical content produces identical hashes across all clones.
func (i *Issue) ComputeContentHash() string {
	h := sha256.New()
	w := hashFieldWriter{h}

	// Core fields in stable order
	w.str(i.Title)
	w.str(i.Description)
	w.str(i.Design)
	w.str(i.DesignArtifactID)
	w.boolField(i.HasDesign, "has_design")
	w.str(i.AcceptanceCriteria)
	w.str(i.Notes)
	w.str(string(i.Status))
	w.int(i.Priority)
	w.str(string(i.IssueType))
	w.str(i.Assignee)
	w.str(i.Owner)
	w.str(i.CreatedBy)

	// Optional fields
	w.strPtr(i.ExternalRef)
	w.str(i.SourceSystem)
	w.flag(i.Pinned, "pinned")
	w.flag(i.IsTemplate, "template")
	w.intPtr(i.EstimatedMinutes)
	w.timePtr(i.DueAt)
	w.timePtr(i.DeferUntil)
	w.str(i.CloseReason)
	w.str(i.ClosedBySession)
	w.str(i.Sender)
	w.str(i.SourceFormula)
	w.str(i.SourceLocation)
	w.boolField(i.Ephemeral, "ephemeral")
	w.str(i.DeletedBy)
	w.str(i.DeleteReason)
	w.str(i.OriginalType)

	// Labels (sorted for order-independence)
	w.sortedStrings(i.Labels)

	// Dependencies (sorted by key for order-independence)
	w.dependencies(i.Dependencies)

	// Comments (sorted by ID for order-independence)
	w.comments(i.Comments)

	// Bonded molecules
	for _, br := range i.BondedFrom {
		w.str(br.SourceID)
		w.str(br.BondType)
		w.str(br.BondPoint)
	}

	// HOP entity tracking
	w.entityRef(i.Creator)

	// HOP validations
	for _, v := range i.Validations {
		w.entityRef(v.Validator)
		w.str(v.Outcome)
		w.str(v.Timestamp.Format(time.RFC3339))
		w.float32Ptr(v.Score)
	}

	// HOP aggregate quality score and crystallizes
	w.float32Ptr(i.QualityScore)
	w.flag(i.Crystallizes, "crystallizes")

	// Gate fields for async coordination
	w.str(i.AwaitType)
	w.str(i.AwaitID)
	w.duration(i.Timeout)
	w.sortedStrings(i.Waiters)

	// Slot fields for exclusive access
	w.str(i.Holder)

	// Agent identity fields
	w.str(string(i.AgentState))
	w.str(i.RoleType)
	w.str(i.Rig)

	// Molecule type
	w.str(string(i.MolType))

	// Work type
	w.str(string(i.WorkType))

	// Event fields
	w.str(i.EventKind)
	w.str(i.Actor)
	w.str(i.Target)
	w.str(i.Payload)

	return fmt.Sprintf("%x", h.Sum(nil))
}

// hashFieldWriter provides helper methods for writing fields to a hash.
// Each method writes the value followed by a null separator for consistency.
type hashFieldWriter struct {
	h hash.Hash
}

func (w hashFieldWriter) str(s string) {
	w.h.Write([]byte(s))
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) int(n int) {
	w.h.Write([]byte(fmt.Sprintf("%d", n)))
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) strPtr(p *string) {
	if p != nil {
		w.h.Write([]byte(*p))
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) float32Ptr(p *float32) {
	if p != nil {
		w.h.Write([]byte(fmt.Sprintf("%f", *p)))
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) duration(d time.Duration) {
	w.h.Write([]byte(fmt.Sprintf("%d", d)))
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) flag(b bool, label string) {
	if b {
		w.h.Write([]byte(label))
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) intPtr(p *int) {
	if p != nil {
		w.h.Write([]byte(fmt.Sprintf("set:%d", *p)))
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) timePtr(t *time.Time) {
	if t != nil {
		w.h.Write([]byte("set:" + t.Format(time.RFC3339Nano)))
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) boolField(b bool, label string) {
	if b {
		w.h.Write([]byte(label))
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) sortedStrings(ss []string) {
	sorted := make([]string, len(ss))
	copy(sorted, ss)
	sort.Strings(sorted)
	for _, s := range sorted {
		w.str(s)
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) dependencies(deps []*Dependency) {
	keys := make([]string, 0, len(deps))
	for _, d := range deps {
		keys = append(keys, fmt.Sprintf("%s:%s:%s:%s", d.IssueID, d.DependsOnID, d.Type, d.CreatedBy))
	}
	sort.Strings(keys)
	for _, k := range keys {
		w.str(k)
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) comments(comments []*Comment) {
	type commentKey struct {
		id  int64
		key string
	}
	keys := make([]commentKey, 0, len(comments))
	for _, c := range comments {
		keys = append(keys, commentKey{c.ID, fmt.Sprintf("%d:%s:%s:%s", c.ID, c.IssueID, c.Author, c.Text)})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].id < keys[j].id })
	for _, k := range keys {
		w.str(k.key)
	}
	w.h.Write([]byte{0})
}

func (w hashFieldWriter) entityRef(e *EntityRef) {
	if e != nil {
		w.str(e.Name)
		w.str(e.Platform)
		w.str(e.Org)
		w.str(e.ID)
	}
}

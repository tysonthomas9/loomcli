package domain

import (
	"encoding/json"
	"testing"
)

func TestSessionEvalMapSchemaDecodesLegacyObjectShape(t *testing.T) {
	legacy := []byte(`{"eval_id":"eval-1","scores":{"outcome_success":90,"instruction_adherence":80,"efficiency":70,"tool_use_quality":60},"score_rationales":{"outcome_success":"ok","instruction_adherence":"ok","efficiency":"ok","tool_use_quality":"ok"}}`)
	var got SessionEval
	if err := json.Unmarshal(legacy, &got); err != nil {
		t.Fatalf("decode legacy session eval: %v", err)
	}
	if got.Scores["outcome_success"] != 90 || got.ScoreRationales["tool_use_quality"] != "ok" {
		t.Fatalf("legacy maps = scores=%v rationales=%v", got.Scores, got.ScoreRationales)
	}

	encoded, err := json.Marshal(SessionEval{Scores: SessionEvalScores{"novel_dimension": 77}, ScoreRationales: SessionEvalScoreRationales{"novel_dimension": "measured"}})
	if err != nil {
		t.Fatalf("encode map-shaped session eval: %v", err)
	}
	if !json.Valid(encoded) {
		t.Fatalf("encoded session eval is invalid JSON: %s", encoded)
	}
	var roundTripped SessionEval
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("decode map-shaped session eval: %v", err)
	}
	if roundTripped.Scores["novel_dimension"] != 77 || roundTripped.ScoreRationales["novel_dimension"] != "measured" {
		t.Fatalf("map round trip = scores=%v rationales=%v", roundTripped.Scores, roundTripped.ScoreRationales)
	}
}

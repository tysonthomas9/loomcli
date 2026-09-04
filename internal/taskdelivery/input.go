package taskdelivery

import (
	"encoding/json"
	"fmt"
)

const inputPlanKey = "deliveryPlan"

// WithPlan adds a frozen plan to an object-shaped runner input without
// discarding caller fields.
func WithPlan(input json.RawMessage, plan Plan) (json.RawMessage, error) {
	payload := map[string]any{}
	if len(input) > 0 && string(input) != "null" {
		if err := json.Unmarshal(input, &payload); err != nil {
			return nil, fmt.Errorf("decode task input for delivery plan: %w", err)
		}
	}
	payload[inputPlanKey] = plan
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode task input with delivery plan: %w", err)
	}
	return out, nil
}

func PlanFromInput(input json.RawMessage) (Plan, bool) {
	var payload struct {
		Plan Plan `json:"deliveryPlan"`
	}
	if len(input) == 0 || json.Unmarshal(input, &payload) != nil || payload.Plan.PlanID == "" {
		return Plan{}, false
	}
	return payload.Plan, true
}

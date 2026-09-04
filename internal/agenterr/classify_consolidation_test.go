package agenterr

import "testing"

func TestClassifyFromOutput_WorkScanFailureMarker(t *testing.T) {
	t.Parallel()
	cause := "failed to check ready tasks: HTTP 429 rate limit exceeded"
	aerr := ClassifyFromOutput(WorkScanFailureMarker+": "+cause, 0, "codex")
	if aerr.Class != OutcomeFromDomain(WorkScanFailureOutcome) {
		t.Fatalf("class = %s, want WorkScanFailure", aerr.Class)
	}
	if aerr.Message != cause {
		t.Errorf("message = %q, want %q", aerr.Message, cause)
	}
}

// TestClassifyFromOutput_ConnectionRefusedIsRetryable is the headline fix: a
// bare connection-refused / transport error used to fall through to Unknown
// (not retryable) in both the wrapper and loom's classifier. The wrapper now
// owns transport patterns, so every backend classifies it as a retryable
// transient instead of giving up.
func TestClassifyFromOutput_ConnectionRefusedIsRetryable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		backend string
		output  string
	}{
		{"claude", "Error: connect ECONNREFUSED 127.0.0.1:443"},
		{"cursor", "fetch failed: connection refused"},
		{"opencode", "Error: socket hang up"},
	}
	for _, c := range cases {
		t.Run(c.backend, func(t *testing.T) {
			t.Parallel()
			aerr := ClassifyFromOutput(c.output, 1, c.backend)
			// Retryability of Transient is policy, asserted by the
			// agentpolicy golden table.
			if aerr.Class != Transient {
				t.Errorf("[%s] class = %s, want Transient", c.backend, aerr.Class)
			}
		})
	}
}

// TestClassifyFromOutput_APIErrorCodeMapping verifies structured
// "API Error: <code>" lines flow through the wrapper (which extracts the HTTP
// code) and the adapter maps the code onto the right ErrorClass.
func TestClassifyFromOutput_APIErrorCodeMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		output string
		want   ErrorClass
	}{
		{"401 auth", "API Error: 401 Unauthorized", AuthFailure},
		{"402 billing", "API Error: 402 Payment Required", BillingError},
		{"404 model", "API Error: 404 model not found", ModelNotFound},
		{"429 rate", "API Error: 429 Too Many Requests", RateLimited},
		{"503 transient", "API Error: 503 Service Unavailable", Transient},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			aerr := ClassifyFromOutput(c.output, 1, "claude")
			if aerr.Class != c.want {
				t.Errorf("class = %s, want %s", aerr.Class, c.want)
			}
		})
	}
}

// TestClassifyFromOutput_CursorNumericResidual verifies cursor — which the
// wrapper has no profile for (it falls to the cost/transport default) — still
// classifies bare numeric throttling codes via loom's residual, so it does not
// regress to Unknown.
func TestClassifyFromOutput_CursorNumericResidual(t *testing.T) {
	t.Parallel()
	cases := []struct {
		output string
		want   ErrorClass
	}{
		{"Error: 429 slow down", RateLimited},
		{"Error: 402 out of credits", BillingError},
		{"Error: too many requests", RateLimited},
	}
	for _, c := range cases {
		t.Run(c.output, func(t *testing.T) {
			t.Parallel()
			aerr := ClassifyFromOutput(c.output, 1, "cursor")
			if aerr.Class != c.want {
				t.Errorf("class = %s, want %s", aerr.Class, c.want)
			}
		})
	}
}

// TestClassifyFromOutput_BillingStaysFatal guards fix #4: a budget/billing
// exhaustion under the wrapper's coarse blocked_by_cost status must remain
// fatal (BillingError), not become a retryable rate limit.
func TestClassifyFromOutput_BillingStaysFatal(t *testing.T) {
	t.Parallel()
	aerr := ClassifyFromOutput("Error: your credit balance is too low; quota exceeded", 1, "claude")
	if aerr.Class != BillingError {
		t.Fatalf("class = %s, want BillingError", aerr.Class)
	}
	// Fatality of BillingError is policy, asserted by the agentpolicy golden
	// table (Decide -> StopFatal).
}

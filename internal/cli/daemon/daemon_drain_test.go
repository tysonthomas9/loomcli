package daemon

import (
	"testing"
)

// ---------------------------------------------------------------------------
// applyRestartPolicyDefaults YieldTimeout test
// ---------------------------------------------------------------------------

func TestApplyRestartPolicyDefaults_YieldTimeout(t *testing.T) {
	t.Run("nil gets default", func(t *testing.T) {
		rp := RestartPolicy{}
		applyRestartPolicyDefaults(&rp)

		if rp.YieldTimeout == nil {
			t.Fatal("YieldTimeout is nil after applyDefaults, want DefaultYieldTimeout")
		}
		if *rp.YieldTimeout != DefaultYieldTimeout {
			t.Errorf("YieldTimeout = %d, want %d", *rp.YieldTimeout, DefaultYieldTimeout)
		}
	})

	t.Run("already set is preserved", func(t *testing.T) {
		rp := RestartPolicy{YieldTimeout: intPtr(200)}
		applyRestartPolicyDefaults(&rp)

		if *rp.YieldTimeout != 200 {
			t.Errorf("YieldTimeout = %d, want 200 (should not be overwritten)", *rp.YieldTimeout)
		}
	})
}

// ---------------------------------------------------------------------------
// applyRestartPolicyDefaults SigtermTimeout test
// ---------------------------------------------------------------------------

func TestApplyRestartPolicyDefaults_SigtermTimeout(t *testing.T) {
	t.Run("nil gets default", func(t *testing.T) {
		rp := RestartPolicy{}
		applyRestartPolicyDefaults(&rp)

		if rp.SigtermTimeout == nil {
			t.Fatal("SigtermTimeout is nil after applyDefaults, want DefaultSigtermTimeout")
		}
		if *rp.SigtermTimeout != DefaultSigtermTimeout {
			t.Errorf("SigtermTimeout = %d, want %d", *rp.SigtermTimeout, DefaultSigtermTimeout)
		}
	})

	t.Run("already set is preserved", func(t *testing.T) {
		rp := RestartPolicy{SigtermTimeout: intPtr(200)}
		applyRestartPolicyDefaults(&rp)

		if *rp.SigtermTimeout != 200 {
			t.Errorf("SigtermTimeout = %d, want 200 (should not be overwritten)", *rp.SigtermTimeout)
		}
	})
}

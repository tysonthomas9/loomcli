package config

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// ---------------------------------------------------------------------------
// overlayRestartPolicy YieldTimeout tests
// ---------------------------------------------------------------------------

func TestOverlayRestartPolicy_YieldTimeout(t *testing.T) {
	t.Run("src sets YieldTimeout on nil dst", func(t *testing.T) {
		dst := RestartPolicy{}
		src := RestartPolicy{YieldTimeout: IntPtr(120)}
		overlayRestartPolicy(&dst, &src)

		if dst.YieldTimeout == nil {
			t.Fatal("dst.YieldTimeout is nil, want 120")
		}
		if *dst.YieldTimeout != 120 {
			t.Errorf("dst.YieldTimeout = %d, want 120", *dst.YieldTimeout)
		}
	})

	t.Run("src nil does not overwrite dst", func(t *testing.T) {
		dst := RestartPolicy{YieldTimeout: IntPtr(90)}
		src := RestartPolicy{} // YieldTimeout is nil
		overlayRestartPolicy(&dst, &src)

		if dst.YieldTimeout == nil {
			t.Fatal("dst.YieldTimeout became nil, should remain 90")
		}
		if *dst.YieldTimeout != 90 {
			t.Errorf("dst.YieldTimeout = %d, want 90 (should not be overwritten)", *dst.YieldTimeout)
		}
	})

	t.Run("src overwrites existing dst", func(t *testing.T) {
		dst := RestartPolicy{YieldTimeout: IntPtr(60)}
		src := RestartPolicy{YieldTimeout: IntPtr(180)}
		overlayRestartPolicy(&dst, &src)

		if dst.YieldTimeout == nil {
			t.Fatal("dst.YieldTimeout is nil, want 180")
		}
		if *dst.YieldTimeout != 180 {
			t.Errorf("dst.YieldTimeout = %d, want 180", *dst.YieldTimeout)
		}
	})
}

// ---------------------------------------------------------------------------
// overlayRestartPolicy NoWorkBackoffMax tests
// ---------------------------------------------------------------------------

func TestOverlayRestartPolicy_NoWorkBackoffMax(t *testing.T) {
	t.Run("src sets NoWorkBackoffMax on nil dst", func(t *testing.T) {
		dst := RestartPolicy{}
		src := RestartPolicy{NoWorkBackoffMax: IntPtr(600)}
		overlayRestartPolicy(&dst, &src)

		if dst.NoWorkBackoffMax == nil {
			t.Fatal("dst.NoWorkBackoffMax is nil, want 600")
		}
		if *dst.NoWorkBackoffMax != 600 {
			t.Errorf("dst.NoWorkBackoffMax = %d, want 600", *dst.NoWorkBackoffMax)
		}
	})

	t.Run("src nil does not overwrite dst", func(t *testing.T) {
		dst := RestartPolicy{NoWorkBackoffMax: IntPtr(900)}
		src := RestartPolicy{} // NoWorkBackoffMax is nil
		overlayRestartPolicy(&dst, &src)

		if dst.NoWorkBackoffMax == nil {
			t.Fatal("dst.NoWorkBackoffMax became nil, should remain 900")
		}
		if *dst.NoWorkBackoffMax != 900 {
			t.Errorf("dst.NoWorkBackoffMax = %d, want 900 (should not be overwritten)", *dst.NoWorkBackoffMax)
		}
	})

	t.Run("src overwrites existing dst", func(t *testing.T) {
		dst := RestartPolicy{NoWorkBackoffMax: IntPtr(300)}
		src := RestartPolicy{NoWorkBackoffMax: IntPtr(1200)}
		overlayRestartPolicy(&dst, &src)

		if dst.NoWorkBackoffMax == nil {
			t.Fatal("dst.NoWorkBackoffMax is nil, want 1200")
		}
		if *dst.NoWorkBackoffMax != 1200 {
			t.Errorf("dst.NoWorkBackoffMax = %d, want 1200", *dst.NoWorkBackoffMax)
		}
	})
}

func TestRestartPolicyFromDomain_NoWorkBackoffMax(t *testing.T) {
	val := 1200
	rp := restartPolicyFromDomain(domain.RestartPolicy{NoWorkBackoffMax: &val})
	if rp.NoWorkBackoffMax == nil {
		t.Fatal("NoWorkBackoffMax is nil after restartPolicyFromDomain")
	}
	if *rp.NoWorkBackoffMax != val {
		t.Errorf("NoWorkBackoffMax = %d, want %d", *rp.NoWorkBackoffMax, val)
	}
	// Independence: mutating the source pointer must not alias the clone.
	val = 1
	if *rp.NoWorkBackoffMax != 1200 {
		t.Errorf("restartPolicyFromDomain did not clone NoWorkBackoffMax: aliased on source mutation")
	}
}

// ---------------------------------------------------------------------------
// overlayRestartPolicy SigtermTimeout tests
// ---------------------------------------------------------------------------

func TestOverlayRestartPolicy_SigtermTimeout(t *testing.T) {
	t.Run("src sets SigtermTimeout on nil dst", func(t *testing.T) {
		dst := RestartPolicy{}
		src := RestartPolicy{SigtermTimeout: IntPtr(120)}
		overlayRestartPolicy(&dst, &src)

		if dst.SigtermTimeout == nil {
			t.Fatal("dst.SigtermTimeout is nil, want 120")
		}
		if *dst.SigtermTimeout != 120 {
			t.Errorf("dst.SigtermTimeout = %d, want 120", *dst.SigtermTimeout)
		}
	})

	t.Run("src nil does not overwrite dst", func(t *testing.T) {
		dst := RestartPolicy{SigtermTimeout: IntPtr(90)}
		src := RestartPolicy{}
		overlayRestartPolicy(&dst, &src)

		if dst.SigtermTimeout == nil {
			t.Fatal("dst.SigtermTimeout became nil, should remain 90")
		}
		if *dst.SigtermTimeout != 90 {
			t.Errorf("dst.SigtermTimeout = %d, want 90 (should not be overwritten)", *dst.SigtermTimeout)
		}
	})

	t.Run("src overwrites existing dst", func(t *testing.T) {
		dst := RestartPolicy{SigtermTimeout: IntPtr(60)}
		src := RestartPolicy{SigtermTimeout: IntPtr(180)}
		overlayRestartPolicy(&dst, &src)

		if dst.SigtermTimeout == nil {
			t.Fatal("dst.SigtermTimeout is nil, want 180")
		}
		if *dst.SigtermTimeout != 180 {
			t.Errorf("dst.SigtermTimeout = %d, want 180", *dst.SigtermTimeout)
		}
	})
}

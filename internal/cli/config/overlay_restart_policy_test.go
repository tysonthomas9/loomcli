package config

import "testing"

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

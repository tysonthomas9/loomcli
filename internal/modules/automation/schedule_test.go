package automation

import (
	"errors"
	"testing"
	"time"
)

func TestSchedulePolicy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		schedule string
		valid    bool
	}{
		{name: "standard", schedule: "*/5 * * * *", valid: true},
		{name: "descriptor", schedule: "@hourly", valid: true},
		{name: "seconds rejected", schedule: "0 */5 * * * *", valid: false},
		{name: "empty", schedule: "", valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSchedule(test.schedule)
			if test.valid && err != nil {
				t.Fatalf("ValidateSchedule(%q) = %v", test.schedule, err)
			}
			if !test.valid && !errors.Is(err, ErrInvalidSchedule) {
				t.Fatalf("ValidateSchedule(%q) = %v, want ErrInvalidSchedule", test.schedule, err)
			}
		})
	}

	if err := ValidateScheduleTimezone(""); err != nil {
		t.Fatalf("UTC default: %v", err)
	}
	if err := ValidateScheduleTimezone("America/Los_Angeles"); err != nil {
		t.Fatalf("IANA timezone: %v", err)
	}
	if err := ValidateScheduleTimezone("Mars/Olympus"); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("invalid timezone = %v, want ErrInvalidSchedule", err)
	}
}

func TestNextScheduledFireUsesBindingTimezone(t *testing.T) {
	after := time.Date(2026, 1, 15, 16, 30, 0, 0, time.UTC)
	got, err := NextScheduledFire("0 9 * * *", "America/Los_Angeles", after)
	if err != nil {
		t.Fatalf("NextScheduledFire: %v", err)
	}
	want := time.Date(2026, 1, 15, 17, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("NextScheduledFire = %s, want %s", got, want)
	}
}

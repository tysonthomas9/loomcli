package automation

import (
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// ErrInvalidSchedule identifies malformed Automation schedule policy.
var ErrInvalidSchedule = errors.New("invalid cron schedule")

// ValidateSchedule validates the grammar accepted by Automation bindings.
func ValidateSchedule(schedule string) error {
	_, err := parseSchedule(schedule)
	return err
}

// ValidateScheduleTimezone validates an optional IANA timezone. Empty means
// UTC, matching binding creation and update behavior.
func ValidateScheduleTimezone(timezone string) error {
	_, err := scheduleLocation(timezone)
	return err
}

// NextScheduledFire returns the first occurrence strictly after after.
func NextScheduledFire(schedule, timezone string, after time.Time) (time.Time, error) {
	parsed, err := parseSchedule(schedule)
	if err != nil {
		return time.Time{}, err
	}
	location, err := scheduleLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.Next(after.In(location)), nil
}

func parseSchedule(schedule string) (cron.Schedule, error) {
	parsed, err := cron.ParseStandard(schedule)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %v", ErrInvalidSchedule, schedule, err)
	}
	return parsed, nil
}

func scheduleLocation(timezone string) (*time.Location, error) {
	if timezone == "" {
		return time.UTC, nil
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("%w: timezone %q: %v", ErrInvalidSchedule, timezone, err)
	}
	return location, nil
}

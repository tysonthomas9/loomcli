package supervisor

import "github.com/tysonthomas9/loomcli/internal/clock"

// clk is the supervisor's time source for ownership validity, heartbeat
// cadence, liveness ticks and kill deadlines. A nil Supervisor.Clock means the
// real clock, so existing construction sites keep production behavior.
func (s *Supervisor) clk() clock.Clock { return clock.Or(s.Clock) }

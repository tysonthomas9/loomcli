package exe

import "github.com/tysonthomas9/loomcli/internal/placement"

var (
	_ placement.Provider       = (*Provider)(nil)
	_ placement.ParkingCapable = (*Provider)(nil)
)

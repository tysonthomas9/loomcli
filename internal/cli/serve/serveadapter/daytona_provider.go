package serveadapter

import "github.com/tysonthomas9/loomcli/internal/cli/serve/serveadapter/daytonabroker"

type DaytonaProviderBroker = daytonabroker.DaytonaProviderBroker

func NewDaytonaProviderBroker(dataDir string) *DaytonaProviderBroker {
	return daytonabroker.NewDaytonaProviderBroker(dataDir)
}

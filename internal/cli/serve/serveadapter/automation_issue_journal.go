package serveadapter

import (
	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/serveadapter/automationjournal"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

func NewAutomationIssueJournalEmitter(
	emitter systemeventing.IssueJournalEmitter,
	awaits *trigger.AwaitMatcher,
) trigger.InternalEventEmitter {
	return automationjournal.NewAutomationIssueJournalEmitter(emitter, awaits)
}

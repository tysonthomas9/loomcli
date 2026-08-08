package execution

// The run.finished lifecycle contract is owned by Execution. Automation may
// consume this durable outcome vocabulary, but Execution must never depend on
// Automation to define the events it emits.
const (
	RunFinishedEventType           = "run.finished"
	RunFinishedActorRef            = "system"
	RunFinishedSourceEventIDPrefix = "run-finished:"
	RunFinishedSourceKind          = "execution"
)

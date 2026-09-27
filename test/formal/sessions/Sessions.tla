----------------------------- MODULE Sessions -----------------------------
\* Bounded session-record protocol, based on LoomCLI v5 28e657bdc.
\* This is an abstract transition model, not a FleetDB/runtime execution.
\* Daemon finalization and lease liveness belong to daemon-attempt stage d.
EXTENDS Naturals, TLC

CONSTANTS TerminalGuard, AttemptFence, FinishBarrier, ParentSweep,
          NoticeLeaseLoss, NotifyOnFinish, PollEnabled, LivenessHeal,
          AllowCrash, AllowNewAttempt, AllowLeaseLoss, MaxTime
ASSUME /\ MaxTime \in Nat /\ MaxTime >= 2
       /\ \A v \in {TerminalGuard, AttemptFence, FinishBarrier,
                    ParentSweep, NoticeLeaseLoss, NotifyOnFinish,
                    PollEnabled, LivenessHeal, AllowCrash,
                    AllowNewAttempt, AllowLeaseLoss} : v \in BOOLEAN

VARIABLES now, parent, session, sessionAttempt, attempt, lease,
          ctx, finishPending, finishFailed, heartbeatFailed, noticed,
          notifyPending, ui, local, localLive, localAge,
          terminalRewrite, crossAttemptClose, falseHeal, openedAfterParentTerminal,
          issued, completionPending, lateWrite
vars == <<now, parent, session, sessionAttempt, attempt, lease,
          ctx, finishPending, finishFailed, heartbeatFailed, noticed,
          notifyPending, ui, local, localLive, localAge,
          terminalRewrite, crossAttemptClose, falseHeal, openedAfterParentTerminal,
          issued, completionPending, lateWrite>>
Terminal == {"completed", "failed", "cancelled"}
SessionTerminal == {"completed", "failed", "aborted"}

Init ==
  /\ now = 0 /\ parent = "running" /\ session = "none"
  /\ sessionAttempt = 0 /\ attempt = 1 /\ lease = TRUE
  /\ ctx = "open" /\ finishPending = FALSE /\ finishFailed = FALSE
  /\ heartbeatFailed = FALSE /\ noticed = FALSE
  /\ notifyPending = FALSE /\ ui = "running"
  /\ local = "running" /\ localLive = TRUE /\ localAge = 0
  /\ terminalRewrite = FALSE /\ crossAttemptClose = FALSE
  /\ falseHeal = FALSE /\ openedAfterParentTerminal = FALSE /\ issued = FALSE
  /\ completionPending = FALSE /\ lateWrite = FALSE

OpenSession ==
  /\ parent = "running" /\ lease
  /\ session = "none"
  /\ session' = "running" /\ sessionAttempt' = attempt
  /\ UNCHANGED <<now, parent, attempt, lease, ctx, finishPending,
                 finishFailed, heartbeatFailed, noticed, notifyPending,
                 ui, local, localLive, localAge, terminalRewrite,
                 crossAttemptClose, falseHeal, openedAfterParentTerminal, issued, completionPending, lateWrite>>

\* ErrAlreadyExists: legacy writes running even when the row is terminal.
ReopenExisting ==
  /\ session # "none"
  /\ IF TerminalGuard
        THEN /\ parent = "running" /\ session = "running"
             /\ sessionAttempt = attempt /\ lease
        ELSE TRUE
  /\ session' = "running"
  /\ terminalRewrite' = (terminalRewrite \/ session \in SessionTerminal)
  /\ openedAfterParentTerminal' = (openedAfterParentTerminal \/ parent \in Terminal)
  /\ UNCHANGED <<now, parent, sessionAttempt, attempt, lease, ctx,
                 finishPending, finishFailed, heartbeatFailed, noticed,
                 notifyPending, ui, local, localLive, localAge,
                 crossAttemptClose, falseHeal, issued, completionPending, lateWrite>>

RunFinish ==
  /\ parent = "running"
  /\ parent' = "completed" /\ finishPending' = TRUE
  /\ ctx' = "cancelled"
  /\ UNCHANGED <<now, session, sessionAttempt, attempt, lease,
                 finishFailed, heartbeatFailed, noticed, notifyPending,
                 ui, local, localLive, localAge, terminalRewrite,
                 crossAttemptClose, falseHeal, openedAfterParentTerminal, issued, completionPending, lateWrite>>

CrashBeforeFinish ==
  /\ AllowCrash /\ parent = "running" /\ session = "running"
  /\ parent' = "completed" /\ finishPending' = FALSE
  /\ UNCHANGED <<now, session, sessionAttempt, attempt, lease, ctx,
                 finishFailed, heartbeatFailed, noticed, notifyPending,
                 ui, local, localLive, localAge, terminalRewrite,
                 crossAttemptClose, falseHeal, openedAfterParentTerminal, issued, completionPending, lateWrite>>

\* A stale attempt can race a new attempt's open/finish.
NewAttempt ==
  /\ AllowNewAttempt /\ parent = "running" /\ attempt = 1
  /\ attempt' = 2 /\ lease' = TRUE
  /\ UNCHANGED <<now, parent, session, sessionAttempt, ctx,
                 finishPending, finishFailed, heartbeatFailed, noticed,
                 notifyPending, ui, local, localLive, localAge,
                 terminalRewrite, crossAttemptClose, falseHeal,
                 openedAfterParentTerminal, issued, completionPending, lateWrite>>

FinishSession(a) ==
  /\ a \in {1, 2} /\ session = "running"
  /\ IF AttemptFence THEN a = attempt /\ a = sessionAttempt ELSE TRUE
  /\ ~noticed
  /\ finishPending
  /\ IF ctx = "cancelled" /\ ~FinishBarrier
        THEN /\ finishFailed' = TRUE
             /\ UNCHANGED <<session, crossAttemptClose, notifyPending,
                            issued, completionPending, lateWrite>>
        ELSE /\ session' = "completed"
             /\ crossAttemptClose' = (crossAttemptClose \/ a # sessionAttempt)
             /\ completionPending' = TRUE
             /\ lateWrite' = (lateWrite \/ (heartbeatFailed /\ ~lease))
             /\ finishFailed' = FALSE
             /\ UNCHANGED <<notifyPending, issued>>
  /\ finishPending' = FALSE
  /\ UNCHANGED <<now, parent, sessionAttempt, attempt, lease, ctx,
                 heartbeatFailed, noticed, ui, local, localLive, localAge,
                 terminalRewrite, falseHeal, openedAfterParentTerminal>>

\* Reconciliation covers both the missed defer and a terminal parent already
\* present at serve startup. A successful write is guarded by row state.
TerminalParentSweep ==
  /\ ParentSweep /\ parent \in Terminal /\ session = "running"
  /\ session' = "completed" /\ completionPending' = TRUE
  /\ UNCHANGED <<now, parent, sessionAttempt, attempt, lease, ctx,
                 finishPending, finishFailed, heartbeatFailed, noticed,
                 ui, local, localLive, localAge, terminalRewrite,
                 crossAttemptClose, falseHeal, openedAfterParentTerminal,
                 notifyPending, issued, lateWrite>>

\* Completion notification is a separate, fair attempt after persistence.
\* #682 legacy omits this path; polling still eventually refreshes a mounted UI.
EmitCompletion ==
  /\ NotifyOnFinish /\ completionPending /\ session \in SessionTerminal
  /\ notifyPending' = TRUE /\ issued' = TRUE
  /\ completionPending' = FALSE
  /\ UNCHANGED <<now, parent, session, sessionAttempt, attempt, lease,
                 ctx, finishPending, finishFailed, heartbeatFailed, noticed,
                 ui, local, localLive, localAge, terminalRewrite,
                 crossAttemptClose, falseHeal, openedAfterParentTerminal,
                 lateWrite>>

LoseLease ==
  /\ AllowLeaseLoss /\ lease /\ session = "running"
  /\ lease' = FALSE
  /\ UNCHANGED <<now, parent, session, sessionAttempt, attempt, ctx,
                 finishPending, finishFailed, heartbeatFailed, noticed,
                 notifyPending, ui, local, localLive, localAge,
                 terminalRewrite, crossAttemptClose, falseHeal,
                 openedAfterParentTerminal, issued, completionPending, lateWrite>>

Heartbeat ==
  /\ ~lease /\ ~heartbeatFailed
  /\ heartbeatFailed' = TRUE /\ noticed' = NoticeLeaseLoss
  /\ UNCHANGED <<now, parent, session, sessionAttempt, attempt, lease,
                 ctx, finishPending, finishFailed, notifyPending, ui,
                 local, localLive, localAge, terminalRewrite,
                 crossAttemptClose, falseHeal, openedAfterParentTerminal, issued, completionPending, lateWrite>>

Notify ==
  /\ notifyPending
  /\ ui' = session /\ notifyPending' = FALSE
  /\ UNCHANGED <<now, parent, session, sessionAttempt, attempt, lease,
                 ctx, finishPending, finishFailed, heartbeatFailed,
                 noticed, local, localLive, localAge, terminalRewrite,
                 crossAttemptClose, falseHeal, openedAfterParentTerminal, issued, completionPending, lateWrite>>

DropNotify ==
  /\ notifyPending
  /\ notifyPending' = FALSE
  /\ UNCHANGED <<now, parent, session, sessionAttempt, attempt, lease,
                 ctx, finishPending, finishFailed, heartbeatFailed,
                 noticed, ui, local, localLive, localAge, terminalRewrite,
                 crossAttemptClose, falseHeal, openedAfterParentTerminal, issued, completionPending, lateWrite>>

Poll ==
  /\ PollEnabled /\ ui # session /\ session \in SessionTerminal
  /\ ui' = session
  /\ UNCHANGED <<now, parent, session, sessionAttempt, attempt, lease,
                 ctx, finishPending, finishFailed, heartbeatFailed,
                 noticed, notifyPending, local, localLive, localAge,
                 terminalRewrite, crossAttemptClose, falseHeal,
                 openedAfterParentTerminal, issued, completionPending, lateWrite>>

Tick ==
  /\ now < MaxTime
  /\ now' = now + 1 /\ localAge' = localAge + 1
  /\ UNCHANGED <<parent, session, sessionAttempt, attempt, lease,
                 ctx, finishPending, finishFailed, heartbeatFailed,
                 noticed, notifyPending, ui, local, localLive,
                 terminalRewrite, crossAttemptClose, falseHeal,
                 openedAfterParentTerminal, issued, completionPending, lateWrite>>

LocalStop ==
  /\ localLive /\ local = "running"
  /\ localLive' = FALSE
  /\ UNCHANGED <<now, parent, session, sessionAttempt, attempt, lease,
                 ctx, finishPending, finishFailed, heartbeatFailed,
                 noticed, notifyPending, ui, local, localAge,
                 terminalRewrite, crossAttemptClose, falseHeal,
                 openedAfterParentTerminal, issued, completionPending, lateWrite>>

Heal ==
  /\ local = "running" /\ localAge >= 2
  /\ IF LivenessHeal THEN ~localLive ELSE TRUE
  /\ local' = "aborted" /\ falseHeal' = (falseHeal \/ localLive)
  /\ UNCHANGED <<now, parent, session, sessionAttempt, attempt, lease,
                 ctx, finishPending, finishFailed, heartbeatFailed,
                 noticed, notifyPending, ui, localLive, localAge,
                 terminalRewrite, crossAttemptClose,
                 openedAfterParentTerminal, issued, completionPending, lateWrite>>

LocalFinalize ==
  /\ local = "running"
  /\ local' = "completed"
  /\ UNCHANGED <<now, parent, session, sessionAttempt, attempt, lease,
                 ctx, finishPending, finishFailed, heartbeatFailed,
                 noticed, notifyPending, ui, localLive, localAge,
                 terminalRewrite, crossAttemptClose, falseHeal,
                 openedAfterParentTerminal, issued, completionPending, lateWrite>>

Next == OpenSession \/ ReopenExisting \/ RunFinish \/ CrashBeforeFinish \/ NewAttempt
        \/ (\E a \in {1, 2}: FinishSession(a))
        \/ TerminalParentSweep \/ EmitCompletion \/ LoseLease \/ Heartbeat
        \/ Notify \/ DropNotify \/ Poll \/ Tick \/ LocalStop \/ Heal \/ LocalFinalize

Spec == Init /\ [][Next]_vars
        /\ WF_vars(TerminalParentSweep) /\ WF_vars(Notify)
        /\ WF_vars(Poll) /\ WF_vars(Heartbeat)
        /\ WF_vars(EmitCompletion)
        /\ WF_vars(FinishSession(1)) /\ WF_vars(FinishSession(2))

TypeOK == /\ now \in 0..MaxTime /\ parent \in {"running"} \cup Terminal
          /\ session \in {"none", "running"} \cup SessionTerminal
          /\ sessionAttempt \in {0, 1, 2} /\ attempt \in {1, 2}
          /\ lease \in BOOLEAN /\ ctx \in {"open", "cancelled"}
          /\ finishPending \in BOOLEAN /\ finishFailed \in BOOLEAN
          /\ heartbeatFailed \in BOOLEAN /\ noticed \in BOOLEAN
          /\ notifyPending \in BOOLEAN
          /\ ui \in {"running", "none"} \cup SessionTerminal
          /\ local \in {"running"} \cup SessionTerminal
          /\ localLive \in BOOLEAN /\ localAge \in Nat
          /\ terminalRewrite \in BOOLEAN /\ crossAttemptClose \in BOOLEAN
          /\ falseHeal \in BOOLEAN /\ openedAfterParentTerminal \in BOOLEAN
          /\ issued \in BOOLEAN /\ completionPending \in BOOLEAN
          /\ lateWrite \in BOOLEAN
TaskSessionTerminalOnce == ~terminalRewrite
NoCrossAttemptClose == ~crossAttemptClose
NoWriteAfterDriverLeaseLoss == ~lateWrite
NoHealWhileLive == ~falseHeal
NoOpenAfterParentTerminal == ~openedAfterParentTerminal
NoFinishFailure == ~finishFailed
CompletionNotificationIssued == session \in SessionTerminal ~> issued
SessionTerminatesWithParent == (parent \in Terminal /\ session = "running") ~> session \in SessionTerminal
CompletionEventuallyVisible == session \in SessionTerminal ~> ui \in SessionTerminal
=============================================================================

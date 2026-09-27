------------------------------- MODULE Locks --------------------------------
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Mode, Legacy
Processes == {1, 2, 3}
ASSUME Mode \in {"agent", "daemon", "registry", "registry-label"}
ASSUME Legacy \in BOOLEAN

VARIABLES path, nextInode, fd, inodeOwner, phase, live,
          recordPID, recordBirth, recordAge, observed, sidecar,
          pidBirth, advertised, detected
vars == <<path, nextInode, fd, inodeOwner, phase, live,
          recordPID, recordBirth, recordAge, observed, sidecar,
          pidBirth, advertised, detected>>

Init ==
  /\ path = IF Mode = "agent" THEN 1 ELSE 0
  /\ nextInode = 2
  /\ fd = [p \in Processes |-> 0]
  /\ inodeOwner = [i \in 1..4 |-> 0]
  /\ phase = [p \in Processes |-> "ready"]
  /\ live = [p \in Processes |-> FALSE]
  /\ recordPID = IF Mode = "registry-label" THEN 0 ELSE 9
  /\ recordBirth = 1
  /\ recordAge = 2
  /\ observed = [p \in Processes |-> 0]
  /\ sidecar = 0
  /\ pidBirth = 0
  /\ advertised = (Mode # "registry-label")
  /\ detected = FALSE

\* A stale agent record has PID 9 and age 2. A sidecar lock protects the
\* check/remove/create transaction; the legacy path has no such guard.
TakeSidecar(p) ==
  /\ Mode = "agent" /\ ~Legacy /\ sidecar = 0
  /\ phase[p] = "ready"
  /\ sidecar' = p
  /\ UNCHANGED <<path, nextInode, fd, inodeOwner, phase, live,
                recordPID, recordBirth, recordAge, observed, pidBirth,
                advertised, detected>>

Observe(p) ==
  /\ Mode = "agent" /\ phase[p] = "ready"
  /\ (Legacy \/ sidecar = p)
  /\ observed' = [observed EXCEPT ![p] = IF recordAge >= 2 /\
                                             (recordPID = 9 \/ ~live[recordPID])
                                            THEN path ELSE 0]
  /\ phase' = [phase EXCEPT ![p] = "checked"]
  /\ UNCHANGED <<path, nextInode, fd, inodeOwner, live, recordPID,
                recordBirth, recordAge, sidecar, pidBirth, advertised, detected>>

RemoveStale(p) ==
  /\ Mode = "agent" /\ phase[p] = "checked"
  /\ observed[p] # 0
  /\ (Legacy \/ sidecar = p)
  /\ path' = IF Legacy \/ path = observed[p] THEN 0 ELSE path
  /\ phase' = [phase EXCEPT ![p] = "removed"]
  /\ UNCHANGED <<nextInode, fd, inodeOwner, live, recordPID,
                recordBirth, recordAge, observed, sidecar, pidBirth,
                advertised, detected>>

SkipLive(p) ==
  /\ Mode = "agent" /\ phase[p] = "checked" /\ observed[p] = 0
  /\ phase' = [phase EXCEPT ![p] = "done"]
  /\ sidecar' = IF Legacy THEN sidecar ELSE 0
  /\ UNCHANGED <<path, nextInode, fd, inodeOwner, live, recordPID,
                recordBirth, recordAge, observed, pidBirth, advertised, detected>>

CreateAgent(p) ==
  /\ Mode = "agent" /\ path = 0 /\ nextInode <= 4
  /\ phase[p] \in {"checked", "removed"}
  /\ (Legacy \/ sidecar = p)
  /\ path' = nextInode
  /\ nextInode' = nextInode + 1
  /\ fd' = [fd EXCEPT ![p] = path']
  /\ phase' = [phase EXCEPT ![p] = "holding"]
  /\ live' = [live EXCEPT ![p] = TRUE]
  /\ recordPID' = p
  /\ recordBirth' = recordBirth + 1
  /\ recordAge' = 0
  /\ sidecar' = IF Legacy THEN sidecar ELSE 0
  /\ UNCHANGED <<inodeOwner, observed, pidBirth, advertised, detected>>

OpenDaemon(p) ==
  /\ Mode = "daemon" /\ phase[p] = "ready"
  /\ nextInode <= 4
  /\ path' = IF path = 0 THEN nextInode ELSE path
  /\ nextInode' = IF path = 0 THEN nextInode + 1 ELSE nextInode
  /\ fd' = [fd EXCEPT ![p] = path']
  /\ phase' = [phase EXCEPT ![p] = "opened"]
  /\ UNCHANGED <<inodeOwner, live, recordPID, recordBirth,
                recordAge, observed, sidecar, pidBirth, advertised, detected>>

Flock(p) ==
  /\ Mode = "daemon" /\ phase[p] = "opened"
  /\ inodeOwner[fd[p]] = 0
  /\ inodeOwner' = [inodeOwner EXCEPT ![fd[p]] = p]
  /\ phase' = [phase EXCEPT ![p] = "holding"]
  /\ live' = [live EXCEPT ![p] = TRUE]
  /\ UNCHANGED <<path, nextInode, fd, recordPID, recordBirth,
                recordAge, observed, sidecar, pidBirth, advertised, detected>>

ReleaseDaemon(p) ==
  /\ Mode = "daemon" /\ phase[p] = "holding"
  /\ path' = IF Legacy /\ path = fd[p] THEN 0 ELSE path
  /\ inodeOwner' = [inodeOwner EXCEPT ![fd[p]] = 0]
  /\ phase' = [phase EXCEPT ![p] = "done"]
  /\ live' = [live EXCEPT ![p] = FALSE]
  /\ UNCHANGED <<nextInode, fd, recordPID, recordBirth,
                recordAge, observed, sidecar, pidBirth, advertised, detected>>

Crash(p) ==
  /\ Mode \in {"agent", "daemon"} /\ phase[p] = "holding"
  /\ phase' = [phase EXCEPT ![p] = "done"]
  /\ live' = [live EXCEPT ![p] = FALSE]
  /\ inodeOwner' = IF Mode = "daemon"
                   THEN [inodeOwner EXCEPT ![fd[p]] = 0]
                   ELSE inodeOwner
  /\ UNCHANGED <<path, nextInode, fd, recordPID, recordBirth,
                recordAge, observed, sidecar, pidBirth, advertised, detected>>

AgeRecord ==
  /\ Mode = "agent" /\ recordPID \in Processes
  /\ ~live[recordPID] /\ recordAge < 2
  /\ recordAge' = recordAge + 1
  /\ UNCHANGED <<path, nextInode, fd, inodeOwner, phase, live,
                recordPID, recordBirth, observed, sidecar, pidBirth,
                advertised, detected>>

\* The registry row outlives a crashed daemon. The OS may recycle its PID.
ReusePID ==
  /\ Mode = "registry" /\ pidBirth = 0
  /\ pidBirth' = 2
  /\ UNCHANGED <<path, nextInode, fd, inodeOwner, phase, live,
                recordPID, recordBirth, recordAge, observed, sidecar,
                advertised, detected>>

PublishRegistry ==
  /\ Mode = "registry-label" /\ ~advertised
  /\ advertised' = TRUE
  /\ UNCHANGED <<path, nextInode, fd, inodeOwner, phase, live,
                recordPID, recordBirth, recordAge, observed, sidecar,
                pidBirth, detected>>

ExpireRegistry ==
  /\ Mode = "registry" /\ advertised
  /\ advertised' = FALSE
  /\ UNCHANGED <<path, nextInode, fd, inodeOwner, phase, live,
                recordPID, recordBirth, recordAge, observed, sidecar,
                pidBirth, detected>>

DetectRegistry ==
  /\ Mode \in {"registry", "registry-label"} /\ advertised
  /\ detected' = (Legacy \/ recordPID > 0)
                  /\ (Mode = "registry-label" \/ pidBirth # 0)
                  /\ (Mode = "registry-label" \/ Legacy \/ pidBirth = recordBirth)
  /\ UNCHANGED <<path, nextInode, fd, inodeOwner, phase, live,
                recordPID, recordBirth, recordAge, observed, sidecar,
                pidBirth, advertised>>

Next ==
  \/ \E p \in Processes: TakeSidecar(p) \/ Observe(p) \/ RemoveStale(p)
                         \/ SkipLive(p)
                         \/ CreateAgent(p) \/ OpenDaemon(p) \/ Flock(p)
                         \/ ReleaseDaemon(p) \/ Crash(p)
  \/ AgeRecord \/ ReusePID \/ PublishRegistry \/ ExpireRegistry
  \/ DetectRegistry

AtMostOneHolder == Cardinality({p \in Processes: live[p]}) <= 1
NoLiveTakeover == \A p \in Processes: live[p] => path = fd[p]
NoGhostRegistry == detected => pidBirth = recordBirth
NoUnlabelledDaemon == detected => recordPID > 0
TypeOK == path \in 0..4 /\ nextInode \in 2..5
Spec == Init /\ [][Next]_vars
=============================================================================

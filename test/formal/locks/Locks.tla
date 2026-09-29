------------------------------- MODULE Locks --------------------------------
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Mode, Legacy, CheckPID
Processes == {1, 2, 3}
ASSUME Mode \in {"agent", "daemon"}
ASSUME Legacy \in BOOLEAN
ASSUME CheckPID \in BOOLEAN

VARIABLES path, nextInode, fd, inodeOwner, phase, live,
          recordPID, recordBirth, recordAge, observed, sidecar
vars == <<path, nextInode, fd, inodeOwner, phase, live,
          recordPID, recordBirth, recordAge, observed, sidecar>>

Init ==
  /\ path = IF Mode = "agent" THEN 1 ELSE 0
  /\ nextInode = 2
  /\ fd = [p \in Processes |-> 0]
  /\ inodeOwner = [i \in 1..4 |-> 0]
  /\ phase = [p \in Processes |-> "ready"]
  /\ live = [p \in Processes |-> FALSE]
  /\ recordPID = 9
  /\ recordBirth = 1
  /\ recordAge = 2
  /\ observed = [p \in Processes |-> 0]
  /\ sidecar = 0

\* A stale agent record has PID 9 and age 2. A sidecar lock protects the
\* check/remove/create transaction; the legacy path has no such guard.
TakeSidecar(p) ==
  /\ Mode = "agent" /\ ~Legacy /\ sidecar = 0
  /\ phase[p] = "ready"
  /\ sidecar' = p
  /\ UNCHANGED <<path, nextInode, fd, inodeOwner, phase, live,
                recordPID, recordBirth, recordAge, observed>>

Observe(p) ==
  /\ Mode = "agent" /\ phase[p] = "ready"
  /\ (Legacy \/ sidecar = p)
  /\ observed' = [observed EXCEPT ![p] = IF recordAge >= 2 /\
                                             (recordPID = 9 \/ ~CheckPID \/ ~live[recordPID])
                                            THEN path ELSE 0]
  /\ phase' = [phase EXCEPT ![p] = "checked"]
  /\ UNCHANGED <<path, nextInode, fd, inodeOwner, live, recordPID,
                recordBirth, recordAge, sidecar>>

RemoveStale(p) ==
  /\ Mode = "agent" /\ phase[p] = "checked"
  /\ observed[p] # 0
  /\ (Legacy \/ sidecar = p)
  \* POSIX unlink is unconditional: the sidecar is the entire fix mechanism.
  /\ path' = 0
  /\ phase' = [phase EXCEPT ![p] = "removed"]
  /\ UNCHANGED <<nextInode, fd, inodeOwner, live, recordPID,
                recordBirth, recordAge, observed, sidecar>>

SkipLive(p) ==
  /\ Mode = "agent" /\ phase[p] = "checked" /\ observed[p] = 0
  /\ phase' = [phase EXCEPT ![p] = "done"]
  /\ sidecar' = IF Legacy THEN sidecar ELSE 0
  /\ UNCHANGED <<path, nextInode, fd, inodeOwner, live, recordPID,
                recordBirth, recordAge, observed>>

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
  /\ UNCHANGED <<inodeOwner, observed>>

OpenDaemon(p) ==
  /\ Mode = "daemon" /\ phase[p] = "ready"
  /\ nextInode <= 4
  /\ path' = IF path = 0 THEN nextInode ELSE path
  /\ nextInode' = IF path = 0 THEN nextInode + 1 ELSE nextInode
  /\ fd' = [fd EXCEPT ![p] = path']
  /\ phase' = [phase EXCEPT ![p] = "opened"]
  /\ UNCHANGED <<inodeOwner, live, recordPID, recordBirth,
                recordAge, observed, sidecar>>

Flock(p) ==
  /\ Mode = "daemon" /\ phase[p] = "opened"
  /\ inodeOwner[fd[p]] = 0
  /\ inodeOwner' = [inodeOwner EXCEPT ![fd[p]] = p]
  /\ phase' = [phase EXCEPT ![p] = "holding"]
  /\ live' = [live EXCEPT ![p] = TRUE]
  /\ UNCHANGED <<path, nextInode, fd, recordPID, recordBirth,
                recordAge, observed, sidecar>>

ReleaseDaemon(p) ==
  /\ Mode = "daemon" /\ phase[p] = "holding"
  /\ path' = IF Legacy /\ path = fd[p] THEN 0 ELSE path
  /\ inodeOwner' = [inodeOwner EXCEPT ![fd[p]] = 0]
  /\ phase' = [phase EXCEPT ![p] = "done"]
  /\ live' = [live EXCEPT ![p] = FALSE]
  /\ UNCHANGED <<nextInode, fd, recordPID, recordBirth,
                recordAge, observed, sidecar>>

Crash(p) ==
  /\ Mode \in {"agent", "daemon"} /\ phase[p] = "holding"
  /\ phase' = [phase EXCEPT ![p] = "done"]
  /\ live' = [live EXCEPT ![p] = FALSE]
  /\ inodeOwner' = IF Mode = "daemon"
                   THEN [inodeOwner EXCEPT ![fd[p]] = 0]
                   ELSE inodeOwner
  /\ UNCHANGED <<path, nextInode, fd, recordPID, recordBirth,
                recordAge, observed, sidecar>>

AgeRecord ==
  /\ Mode = "agent" /\ recordPID \in Processes
  /\ recordAge < 2
  /\ recordAge' = recordAge + 1
  /\ UNCHANGED <<path, nextInode, fd, inodeOwner, phase, live,
                recordPID, recordBirth, observed, sidecar>>

Next ==
  \/ \E p \in Processes: TakeSidecar(p) \/ Observe(p) \/ RemoveStale(p)
                         \/ SkipLive(p)
                         \/ CreateAgent(p) \/ OpenDaemon(p) \/ Flock(p)
                         \/ ReleaseDaemon(p) \/ Crash(p)
  \/ AgeRecord

AtMostOneHolder == Cardinality({p \in Processes: live[p]}) <= 1
NoLiveTakeover == \A p \in Processes: live[p] => path = fd[p]
TypeOK == path \in 0..4 /\ nextInode \in 2..5
Spec == Init /\ [][Next]_vars
=============================================================================

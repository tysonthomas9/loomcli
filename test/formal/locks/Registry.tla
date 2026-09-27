------------------------------ MODULE Registry ------------------------------
EXTENDS Naturals, TLC

\* Two daemon PIDs and one loom-serve PID share the node registry. CheckLabel
\* models #281; CheckBirth models the proposed protection against PID reuse.
CONSTANTS CheckLabel, CheckBirth, AllowCrash
ASSUME CheckLabel \in BOOLEAN /\ CheckBirth \in BOOLEAN /\ AllowCrash \in BOOLEAN
Processes == {1, 2, 3}
Daemons == {1, 2}

VARIABLES status, generation, row, label, rowGeneration, readPhase,
          readLabel, readGeneration, result, seenLive, badDetection
vars == <<status, generation, row, label, rowGeneration, readPhase,
          readLabel, readGeneration, result, seenLive, badDetection>>

Init ==
  /\ status = [p \in Processes |-> "off"]
  /\ generation = [p \in Processes |-> 0]
  /\ row = [p \in Processes |-> FALSE]
  /\ label = [p \in Processes |-> FALSE]
  /\ rowGeneration = [p \in Processes |-> 0]
  /\ readPhase = [p \in Processes |-> "idle"]
  /\ readLabel = [p \in Processes |-> FALSE]
  /\ readGeneration = [p \in Processes |-> 0]
  /\ result = [p \in Processes |-> FALSE]
  /\ seenLive = [p \in Daemons |-> FALSE]
  /\ badDetection = FALSE

StartDaemon(p) ==
  /\ p \in Daemons /\ status[p] = "off"
  /\ status' = [status EXCEPT ![p] = "running"]
  /\ generation' = [generation EXCEPT ![p] = 1]
  /\ UNCHANGED <<row, label, rowGeneration, readPhase, readLabel,
                readGeneration, result, seenLive, badDetection>>

StartServe ==
  /\ status[3] = "off"
  /\ status' = [status EXCEPT ![3] = "serve"]
  /\ generation' = [generation EXCEPT ![3] = 1]
  /\ UNCHANGED <<row, label, rowGeneration, readPhase, readLabel,
                readGeneration, result, seenLive, badDetection>>

Publish(p) ==
  /\ p \in Processes /\ ~row[p]
  /\ status[p] \in {"running", "serve"}
  /\ row' = [row EXCEPT ![p] = TRUE]
  /\ label' = [label EXCEPT ![p] = (p \in Daemons)]
  /\ rowGeneration' = [rowGeneration EXCEPT ![p] = generation[p]]
  /\ UNCHANGED <<status, generation, readPhase, readLabel,
                readGeneration, result, seenLive, badDetection>>

Crash(p) ==
  /\ AllowCrash /\ p \in Daemons /\ status[p] = "running"
  /\ status' = [status EXCEPT ![p] = "crashed"]
  /\ UNCHANGED <<generation, row, label, rowGeneration, readPhase,
                readLabel, readGeneration, result, seenLive, badDetection>>

ReusePID(p) ==
  /\ AllowCrash /\ p \in Daemons /\ status[p] = "crashed"
  /\ status' = [status EXCEPT ![p] = "reused"]
  /\ generation' = [generation EXCEPT ![p] = generation[p] + 1]
  /\ UNCHANGED <<row, label, rowGeneration, readPhase, readLabel,
                readGeneration, result, seenLive, badDetection>>

\* Read and process probe are separate so crash/reuse can occur between them.
ReadRow(p) ==
  /\ p \in Processes /\ row[p] /\ readPhase[p] = "idle"
  /\ readPhase' = [readPhase EXCEPT ![p] = "read"]
  /\ readLabel' = [readLabel EXCEPT ![p] = label[p]]
  /\ readGeneration' = [readGeneration EXCEPT ![p] = rowGeneration[p]]
  /\ UNCHANGED <<status, generation, row, label, rowGeneration,
                result, seenLive, badDetection>>

Candidate(p) ==
  /\ (~CheckLabel \/ readLabel[p])
  /\ (IF CheckBirth
      THEN status[p] \in {"running", "serve"} /\ generation[p] = readGeneration[p]
      ELSE status[p] \in {"running", "reused", "serve"})

ActuallyLiveDaemon(p) ==
  p \in Daemons /\ status[p] = "running"
  /\ readLabel[p] /\ generation[p] = readGeneration[p]

Probe(p) ==
  /\ p \in Processes /\ readPhase[p] = "read"
  /\ readPhase' = [readPhase EXCEPT ![p] = "done"]
  /\ result' = [result EXCEPT ![p] = Candidate(p)]
  /\ seenLive' = IF p \in Daemons
                 THEN [seenLive EXCEPT ![p] = @ \/ (Candidate(p) /\ ActuallyLiveDaemon(p))]
                 ELSE seenLive
  /\ badDetection' = (badDetection \/ (Candidate(p) /\ ~ActuallyLiveDaemon(p)))
  /\ UNCHANGED <<status, generation, row, label, rowGeneration,
                readLabel, readGeneration>>

Next ==
  \/ \E p \in Daemons: StartDaemon(p) \/ Crash(p) \/ ReusePID(p)
  \/ StartServe
  \/ \E p \in Processes: Publish(p) \/ ReadRow(p) \/ Probe(p)

Spec == Init /\ [][Next]_vars
\* Liveness applies to a daemon that stays up after publication. Weak
\* fairness makes a continuously enabled read and probe eventually run.
FairSpec == Spec /\ \A p \in Daemons: WF_vars(ReadRow(p)) /\ WF_vars(Probe(p))
NoFalseDetection == ~badDetection
NeverDetected == \A p \in Daemons: ~seenLive[p]
LiveEventuallySeen == \A p \in Daemons:
  (status[p] = "running" /\ row[p]) ~> seenLive[p]
=============================================================================

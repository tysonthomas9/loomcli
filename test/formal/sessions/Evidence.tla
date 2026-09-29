----------------------------- MODULE Evidence -----------------------------
\* Bounded ordering of transcript capture, finalize, and usage publication.
\* The remote artifact and hook failures here are abstract faults; no service
\* was called. The first terminal outcome is checked in Sessions.tla.
EXTENDS Naturals, TLC

CONSTANTS EvidenceBarrier, PreserveUsage, HonestUsage
ASSUME \A v \in {EvidenceBarrier, PreserveUsage, HonestUsage} : v \in BOOLEAN
VARIABLES capture, session, failureReported, hookUsage, closeUsage, publishedUsage,
          fabricated, overwritten, evidenceGap
vars == <<capture, session, failureReported, hookUsage, closeUsage,
          publishedUsage, fabricated, overwritten, evidenceGap>>

Init ==
  /\ capture = "pending" /\ session = "running"
  /\ failureReported = FALSE
  /\ hookUsage = 3 /\ closeUsage = 3 /\ publishedUsage = 3
  /\ fabricated = FALSE /\ overwritten = FALSE /\ evidenceGap = FALSE

Upload ==
  /\ capture = "pending" /\ session = "running"
  /\ capture' = "uploaded"
  /\ UNCHANGED <<session, failureReported, hookUsage, closeUsage,
                 publishedUsage, fabricated, overwritten, evidenceGap>>

UploadFails ==
  /\ capture = "pending" /\ session = "running"
  /\ capture' = "failed"
  /\ UNCHANGED <<session, failureReported, hookUsage, closeUsage,
                 publishedUsage, fabricated, overwritten, evidenceGap>>

HookReports(n) ==
  /\ n \in {0, 1, 2} /\ session = "running"
  /\ hookUsage' = n
  /\ UNCHANGED <<capture, session, failureReported, closeUsage,
                 publishedUsage, fabricated, overwritten, evidenceGap>>

CloseReports(n) ==
  /\ n \in {0, 1, 2} /\ session = "running"
  /\ closeUsage' = n
  /\ UNCHANGED <<capture, session, failureReported, hookUsage,
                 publishedUsage, fabricated, overwritten, evidenceGap>>

Finalize ==
  /\ session = "running"
  /\ IF EvidenceBarrier THEN capture # "pending" ELSE TRUE
  /\ session' = "completed"
  /\ failureReported' = (capture = "failed")
  /\ evidenceGap' = (evidenceGap \/ capture = "pending")
  /\ publishedUsage' =
        IF closeUsage \in {1, 2} THEN closeUsage
        ELSE IF PreserveUsage /\ hookUsage \in {1, 2} THEN hookUsage
        ELSE IF closeUsage = 0 THEN 0
        ELSE IF hookUsage # 3 THEN hookUsage
        ELSE IF HonestUsage THEN 3 ELSE 0
  /\ fabricated' = (fabricated \/ (hookUsage = 3 /\ closeUsage = 3 /\ publishedUsage' = 0))
  /\ overwritten' = (overwritten \/ (hookUsage \in {1, 2} /\ publishedUsage' = 0))
  /\ UNCHANGED <<capture, hookUsage, closeUsage>>

Next == Upload \/ UploadFails \/ (\E n \in {0, 1, 2}: HookReports(n))
        \/ (\E n \in {0, 1, 2}: CloseReports(n)) \/ Finalize
Spec == Init /\ [][Next]_vars

TypeOK == /\ capture \in {"pending", "uploaded", "failed"}
          /\ session \in {"running", "completed"}
          /\ failureReported \in BOOLEAN
          /\ hookUsage \in {3, 0, 1, 2}
          /\ closeUsage \in {3, 0, 1, 2}
          /\ publishedUsage \in {3, 0, 1, 2}
          /\ fabricated \in BOOLEAN /\ overwritten \in BOOLEAN
          /\ evidenceGap \in BOOLEAN
CompletedImpliesEvidenceOrExplicitCaptureFailure == ~evidenceGap
UsageNeverFabricated == ~fabricated
UsageNeverOverwrittenByZero == ~overwritten
=============================================================================

# Artifacts owns evidence policy

Status: accepted

Artifacts owns the canonical evidence format, authorization relationship,
redaction policy, size limits, finalization, and visible failure state. Private
platform adapters may parse backend-specific output and apply the mechanical
redaction operation required by that policy, but platform does not decide what
evidence is durable or who may read it. Run Capture and Transcript Evidence are
read-only projections over lifecycle-owner records and finalized Artifacts;
neither is a second persistence or policy authority.

This split keeps backend format mechanics out of capability and delivery code
without turning the horizontal platform layer into a product-policy owner.

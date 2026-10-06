# Stage 2 independent acceptance

Base 8a01f87, final frozen diff v3. F01/F02/D44.

Completeness `/root/stage1_completeness`: 7/7 = 100%, PASS.
- S2.1: isolated committed checkout; exact staging; source snapshot/index preservation.
- S2.2: exact root/submodule refs, no branch/unrelated tags, atomic-only push.
- S2.3: cleanup, prepare/tag/reject/retry, explicit publication lifecycle; actual
  in-flight remote transaction reports unknown and later publishes safely.
- S2.4: portable Go module edit, relative/root/tracked/namespace validation.
- S2.5: validate includes fixtures; release depends on validate; platform/recovery docs.
- S2.6: all required AAA fixtures and actual review-SHA F01/F02 repros executed.
- S2.7: final 14 fixtures OK in 21.319s, static checks PASS; correctness verdict separately verified below.

Correctness `/root/stage1_correctness`: PASS; no unresolved confirmed scope errors.
Independent signal repro and 12 current fixtures PASS (2 baseline tests skipped
in that separate run); inspected full 14-fixture log with both baseline repros.
Diff whitespace/shell syntax PASS. Isolation, exact staging/refs, atomic push,
rewriting, signing settings, cleanup/outcome and gates/docs reviewed.

Initial P2: interrupted push reported not published while remote still ran.
Resolved through explicit state and conservative unknown on interruption/missing
refs; independent repro confirms eventual remote publication matches the report.
Reconciliation compares exact prepared tag object identities (including signing).
Fixtures disable signing locally; source effective signing settings copied to clone.

Limits: tests used macOS and local bare origins. Linux execution, actual signing
and network transports were not exercised; portability is source-reviewed.
No real repository release or push was performed.

# Immutable review behavioral probes

The `.go.txt` files preserve the original external review probes from
`../ai-libs/reviews/contexty-2026-10-06/repro/`. They are data, not new API tests.
`scripts/reproduce_review.py` extracts review SHA
`2a1a7ab7fb919c436e210c2804dd615e2d80626a` into a temporary checkout, executes
selected unchanged probes there and checks their actual behavioral output.
No changes or refs are written into the source checkout. Stage 3 covers
F03/F04/F05/F11/F12; stage 4 adds F06/F07/F08, and stage 5 adds unchanged
F09/F10 overlay probes with `-race -count=10`. The archive contains temporary
test discovery placeholders; overlay source remains the original review probe.

Run `python3 scripts/reproduce_review.py` from the current checkout.
Outputs are saved in `../stage3/baseline.log`, `../stage4/baseline.log` and
`../stage5/baseline.log`. F01/F02 use the stage 2 local Git release fixtures.

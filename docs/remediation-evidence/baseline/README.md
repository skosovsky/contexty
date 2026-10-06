# Immutable review behavioral probes

The `.go.txt` files preserve the original external review probes from
`../ai-libs/reviews/contexty-2026-10-06/repro/`. They are data, not new API tests.
`scripts/reproduce_review.py` extracts review SHA
`2a1a7ab7fb919c436e210c2804dd615e2d80626a` into a temporary checkout, executes
selected unchanged probes there and checks their actual behavioral output.
No changes or refs are written into the source checkout. Stage 3 covers
F03/F04/F05/F11/F12; later stages attach budget/store evidence.

Run `python3 scripts/reproduce_review.py` from the current checkout.
The stage 3 baseline output is saved in `../stage3/baseline.log`.

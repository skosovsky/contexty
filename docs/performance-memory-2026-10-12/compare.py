"""Compare three sequential samples per fixture; allocation gates only."""
import json
import pathlib
import re
import statistics

ROOT = pathlib.Path(__file__).resolve().parent
PATTERN = re.compile(
    r"^(BenchmarkSelectionArtifact\S+)-\d+\s+\d+\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op$"
)


def samples(name):
    data = {}
    log = (ROOT / name).read_text()
    assert "AUDIT_LOCK_RELEASED 0" in log and "\nPASS\n" in log
    for line in log.splitlines():
        match = PATTERN.match(line)
        if match:
            data.setdefault(match[1], []).append([float(x) for x in match.groups()[1:]])
    assert len(data) == 60, len(data)
    assert all(len(values) == 3 for values in data.values())
    return {key: [statistics.median(col) for col in zip(*values)] for key, values in data.items()}


baseline = samples("baseline-bench.log")
candidate = samples("candidate-bench.log")
assert baseline.keys() == candidate.keys()
rows = []
for name in baseline:
    before, after = baseline[name], candidate[name]
    gate = any(f"/N{n}/A{n}/" in name for n in (100, 300))
    if gate:
        assert after[1] < before[1], (name, "B/op", before, after)
        assert after[2] < before[2], (name, "allocs/op", before, after)
    rows.append({"fixture": name, "baseline": before, "candidate": after, "allocation_gate": gate})
(ROOT / "comparison.json").write_text(json.dumps(rows, indent=2) + "\n")
lines = ["| Fixture | baseline B/op | candidate B/op | baseline allocs/op | candidate allocs/op | ns/op change |",
         "| --- | ---: | ---: | ---: | ---: | ---: |"]
for row in rows:
    before, after = row["baseline"], row["candidate"]
    lines.append(f'| {row["fixture"]} | {before[1]:,.0f} | {after[1]:,.0f} | '
                 f'{before[2]:,.0f} | {after[2]:,.0f} | {100*(after[0]/before[0]-1):+.1f}% |')
(ROOT / "comparison.md").write_text("\n".join(lines) + "\n")
print("PASS: 60 fixtures × 3 samples on each tree; all 10 N=A=100/300 allocation gates pass")

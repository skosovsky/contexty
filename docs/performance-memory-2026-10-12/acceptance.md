# Итоговая приёмка — 2026-10-12

Выполнено. Два независимых субагента приняли одно frozen дерево и одинаковые
итоговые evidence. После verdict исходники, постоянные тесты и общие логи
не менялись.

- [Reviewer A](reviewer-a.md): **APPROVE, 22/22 PASS, полнота 100%**.
- [Reviewer B](reviewer-b.md): **APPROVE, найденных неисправленных ошибок 0**.
- [Root race, regression, lint, formatting и allocation gates](README.md#evidence): PASS.
- [Независимые differential/concurrency probes B](reviewer-b-probes.log): PASS под race.

Baseline: `08b2101e8c05a99f1cf05b2979fdb778b25eed46`.
Source tree SHA256: `73a9410a2beefbb89d92a778b32086de8c9c453513d967237eb8dcc3b4c905e6`.
Diff SHA256: `8e550ccf9da7a77e5d1a818043246fccce762567a55306f188e9996a536942a8`.
[Identity](identity.json), [общие evidence hashes](final-evidence.json),
[hashes финальных verdict](acceptance-hashes.json).

Unmatched preparation N=A=300: 132,242,444 → 1,517,322 B/op (**−98.85%**).
CompileSnapshot N=A=300, четыре targets: 1,648,215,280 → 340,267,136 B/op
(**−79.36%**). Полные таблицы и ограничения — в [отчёте](README.md).

Проверки не являются абсолютным доказательством отсутствия ошибок.
Публикация релиза и изменения consumers не выполнялись.

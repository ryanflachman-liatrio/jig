---
name: dynamic-fanout-synthesize
disable-model-invocation: true
description: Summarizes a [step.foreach] family's ordered aggregate for the dynamic-fanout.toml example.
---

You are the synthesis step of a tiny example workflow that demonstrates jig's
`[step.foreach]` dynamic fan-out. Your input is the `analyze` family's ordered
aggregate JSON (`count`, `succeeded`, `failed`, `all_succeeded`, and a
`results` list with one entry per fanned-out child, in source order).

Respond immediately (no tool use) with a short markdown summary (2-4
sentences) that states: how many children ran, how many succeeded, and the
`instance_id` of each child in order. This step exists only to prove that a
dependent can consume the whole ordered aggregate after every child has
settled — not to perform real analysis.

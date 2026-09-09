---
name: dynamic-fanout-discover
disable-model-invocation: true
description: Produces a small fixed-size list of analysis targets for the dynamic-fanout.toml example's [step.foreach] demonstration.
---

You are the discovery step of a tiny example workflow that demonstrates jig's
`[step.foreach]` dynamic fan-out. Your only job is to emit a short, fixed list
of pretend "analysis targets" — you do not need to read or search the
repository.

Respond immediately (no tool use) with exactly **two** targets:

1. `{"name": "api", "path": "services/api"}`
2. `{"name": "web", "path": "services/web"}`

Set `targets` to that two-element list. This step exists only to prove the
mechanism (a producer's list field feeding a bounded fan-out), not to perform
real analysis.

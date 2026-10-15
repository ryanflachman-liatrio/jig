---
name: research-brief
disable-model-invocation: true
description: Prepare a scoped research brief for the jig research workflow.
---

Turn the user's request into a research brief for five parallel specialists.
Read supplied materials and relevant repository guidance before framing the work.
Ask targeted questions when missing information would change the research;
otherwise state assumptions and proceed. Keep the task scoped to research.

Return a self-contained Markdown brief containing:
- The user's question, decision to support, and success criteria.
- Scope, non-goals, constraints, desired depth, and relevant time horizon.
- Confirmed project/material locations and initial authoritative sources.
- Questions for codebase/architecture, ecosystem/alternatives, interfaces/data,
  security/operations, and testing/feasibility specialists.
- Assumptions, unresolved questions, and evidence needed to settle them.

Keep specialists focused on the request. A domain with little relevance should
explain that briefly rather than invent work. Do not dispatch agents or modify
repository files. The TOML owns scheduling and jig captures your final Markdown
response as the brief artifact.

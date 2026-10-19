---
name: research-brief
disable-model-invocation: true
description: Use for the brief step of the jig research workflow. Do not use for implementation.
---

Turn the user's request into an investigation contract for five parallel
specialists. Keep the subject and deliverables appropriate to the request;
software implementation is only one possible research outcome.

## Frame the decision

Read supplied materials and relevant project guidance. Establish the central
question, the decision or understanding the research must support, its audience,
and observable success criteria. Distinguish user requirements and preferences
from existing knowledge, assumptions, hypotheses, and your recommendations.

Ask targeted questions only when missing information materially changes scope
or the decision. Otherwise state provisional assumptions and proceed. Record
constraints, exclusions, desired depth, and the relevant time horizon.

## Assign answerable questions

Do not merely expand the request into topics. Rank consequential unknowns and
create an investigation matrix with these columns:

question ID | question | decision consequence | owning specialty | priority |
required deliverable | evidence needed for a satisfactory answer

Use stable IDs prefixed CB-Q, ECO-Q, CONTRACTS-Q, RISK-Q, or VAL-Q, followed by a
number. Assign each question a primary owner. Required deliverables should make
the answer assessable: a mechanism trace, comparison against explicit criteria,
worked example, causal risk scenario, or falsifiable experiment as appropriate.
Do not impose arbitrary numbers of findings, sources, or alternatives.

Assess relevance for codebase/architecture, ecosystem/alternatives,
interfaces/data, security/operations, and testing/feasibility. Adapt their lenses
to the subject; mark a specialty not applicable with a reason when appropriate.
Do not invent a codebase, API, security requirement, or implementation project.

## Establish shared context

Confirm supplied project/material locations, source authority, revision/date
where available, and initial evidence. Distinguish inspected sources from leads
that specialists still need to open. For repository work, use repository-relative
paths and identify the project; each specialist uses its own supplied workspace,
not this step's temporary execution view as the shared project root.

Include a small shared baseline of facts already established, with citations
and limitations. Assign overlapping investigations to one primary owner;
independent verification needs a decision-relevant reason. Specialists run in
parallel and cannot consume sibling results. Do not create hidden dependencies
or tell an agent to wait for another branch. Record cross-specialty questions
for the editor to reconcile after all reports arrive.

## Return and check the brief

Return one self-contained Markdown brief with:

1. Central question, decision/audience, and success criteria.
2. Scope, constraints, exclusions, depth, and time horizon.
3. Confirmed materials and cited shared baseline.
4. Investigation matrix and specialty relevance decisions.
5. Assumptions and unresolved questions, distinguishing owner preferences from
   technical or factual unknowns. State whether each blocks a recommendation,
   implementation/action, or publication, and what evidence would settle it.

Before finishing, check that every requested outcome maps to an assigned
question or an explicit exclusion, each important question has an evidence
standard and deliverable, and no parallel branch depends on an unseen result.
Choose depth through coverage and consequences, not a word quota.

Do not dispatch agents, implement changes, or modify repository files. The TOML
owns scheduling and jig captures your final Markdown response as the artifact.

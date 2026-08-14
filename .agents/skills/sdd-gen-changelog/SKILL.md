---
name: sdd-gen-changelog
description: SDD — produce a well-structured CHANGELOG.md entry for a completed implementation and prepend it to the file.
---

You are a Senior Engineer writing the CHANGELOG.md entry for a feature that has just been implemented, validated, and cleared for compliance.

## Step 1 — Gather context

1. Read the spec at `spec_path` — extract the feature name, summary, and key functional requirements.
2. Parse `git_log` to identify commits that were part of this implementation (feat/fix commits since the last merge or tag).
3. Note `compliance_status` (`"pass"` / `"fail"` / `"na"`) from the input.

## Step 2 — Detect CHANGELOG format

Read `CHANGELOG.md` if it exists. Detect the format:
- **Keep a Changelog** — `## [Unreleased]` or `## [1.2.3] — YYYY-MM-DD` pattern → use that format
- **Conventional** — date-only headers → use `## YYYY-MM-DD`
- **None / empty** — use Keep a Changelog format as the default

## Step 3 — Draft the entry

Write the entry in the detected format. Content sections (include only sections that apply):

- **Added** — each major capability delivered, framed as user-visible features (from spec FRs)
- **Changed** — any existing behavior that was modified
- **Fixed** — if the spec addressed a bug or inconsistency
- **Compliance note** (only when `compliance_status == "pass"`): `*Compliance: validated against WCAG/OWASP/PCI/Privacy checks.*`

Entry length: 5–15 bullet points. Each bullet is one sentence, outcome-focused ("Users can now…", "Adds support for…") — not implementation-focused ("Implemented the…").

## Step 4 — Write to CHANGELOG.md

- If `CHANGELOG.md` exists and has `## [Unreleased]`: insert the new entry immediately below that heading.
- If `CHANGELOG.md` exists without `## [Unreleased]`: prepend the new entry below the title line.
- If `CHANGELOG.md` does not exist: create it with the new entry.

## Schema fields

- `changelog_entry` — the drafted entry text (the new section only, not the full file)
- `entry_written` — `true` if CHANGELOG.md was written successfully, `false` otherwise

## What not to do

- Do not invent features not described in the spec or git log.
- Do not include internal implementation details (file names, function names, variable names).
- Do not include commit hashes.
- Do not modify implementation code.

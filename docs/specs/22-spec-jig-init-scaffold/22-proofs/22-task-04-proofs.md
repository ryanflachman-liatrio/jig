# Task 04 Proofs - starter agent scaffold and skill stub

## Task Summary

This task adds the `starter` scaffold as the second data-only registry entry. It emits an agent workflow, a bounded review loop, a deterministic validation gate, and the skill file needed for the loader to accept that workflow.

## What This Task Proves

- `minimal` and `starter` are discoverable without planning or writing files.
- `starter` writes both its workflow and its resolved `.agents/skills/draft/SKILL.md` stub.
- The rendered starter workflow loads through `workflow.Load`, including its skill-front-matter parsing.
- An unknown template is a usage error and lists all valid choices.

## Evidence Summary

- `TestTemplatesValidate` renders and validates every registry entry in a separate temporary target.
- `TestStarterSkillPathAndPrompt` checks the loader-resolved skill path and confirms the parsed agent prompt is non-empty.
- The CLI transcript validates a real generated starter workflow and distinguishes invalid template selection with exit code 2.

## Artifact: starter scaffold CLI flow

**What it proves:** The binary lists both templates, writes the complete starter layout, and validates it with the public CLI.

**Why it matters:** A user receives a complete agent-bearing starting point without manually creating a skill directory or TOML wiring.

**Command:** Build the CLI, run `init --list-templates`, scaffold `starter` into an isolated temporary directory, list emitted assets, and validate the generated workflow.

**Result summary:** Both registry entries are listed. Starter created the workflow and skill stub, and `jig validate` accepted the two-step workflow.

~~~text
minimal: A credential-free command and review workflow.
starter: An agent draft workflow with a bounded review loop.
created /private/tmp/jig-starter-proof.<temp>/.agents/jig/demo.toml
created /private/tmp/jig-starter-proof.<temp>/.agents/skills/draft/SKILL.md
/private/tmp/jig-starter-proof.<temp>/.agents/jig/demo.toml
/private/tmp/jig-starter-proof.<temp>/.agents/skills/draft/SKILL.md
ok: "demo" v1 — 2 step(s)
~~~

## Artifact: unknown-template refusal

**What it proves:** Unknown template selection is reported as a usage error and lists the valid names.

**Why it matters:** An unattended caller can distinguish invalid input from operational failures.

**Result summary:** The CLI printed both valid templates and exited 2.

~~~text
error: unknown scaffold template "nope" (valid templates: minimal, starter)
unknown template exit: 2
~~~

## Artifact: template validation tests

**What it proves:** Every shipped template loads through the production loader; minimal remains credential-free and starter's skill front matter parses.

**Why it matters:** Future schema changes cannot silently ship a broken scaffold.

**Command:**

~~~bash
go test ./internal/scaffold -run 'TestTemplatesValidate|TestMinimalTemplateIsOffline|TestStarterSkillPathAndPrompt' -v
go test ./cmd/jig -run TestInitRejectsUnknownTemplate -v
~~~

**Result summary:** All named tests passed. Repository-wide `go test ./...` and `go vet ./...` also passed; changed Go files produced no `gofmt -l` output.

## Reviewer Conclusion

The registry now has two valid embedded templates. The agent-bearing starter includes the loader-resolved skill stub and demonstrates an inspectable, bounded review loop without introducing template-specific write or collision logic.

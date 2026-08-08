# Task 7 Proofs — Configuration, opt-out, and documentation

## Task Summary

This task proves the security monitoring layer is fully configurable via
`[defaults.security]` / `[step.security]` in the workflow TOML, statically
validated at load time, and documented end-to-end. A zero-config workflow has
security on by default; invalid config values are rejected before any agent
burns a token.

## What This Task Proves

- `SecurityConfig` and `StepSecurity` decode correctly from TOML and cascade
  from `[defaults]` to each step following the same zero-value precedence as
  `model`/`effort`.
- Invalid values (`fleet_budget_usd < 0`, `concurrency_cap < 0`, bad
  hostnames) are rejected at load time with a descriptive error message.
- `examples/feature.toml` with the new `[defaults.security]` block and a
  `[step.security]` per-step override still passes `jig validate`.
- `docs/security-monitoring.md`, the `workflow-schema.md` security section,
  and the ADR are complete.

## Evidence Summary

- `TestSecurityConfig` (7 sub-tests) passes: zero-config, valid cascade,
  and four invalid cases all behave as specified.
- `go run ./cmd/jig validate examples/feature.toml` exits 0.
- Full `go test ./...` suite passes (all packages).

---

## Artifact: TestSecurityConfig test run

**What it proves:** Security config decodes, cascades, and validates correctly
at load time — including zero-config default-on semantics and four invalid-
config rejection cases.

**Why it matters:** This is the primary functional proof for sub-tasks 7.1–7.3.

**Command:**

```bash
rtk proxy go test -v -run TestSecurityConfig ./internal/workflow/
```

**Result summary:** 7 sub-tests pass; invalid values produce the expected
error substrings; zero-config leaves `Defaults.Security.Enabled` nil (engine
default on).

```
=== RUN   TestSecurityConfig
=== RUN   TestSecurityConfig/zero_config_has_security_enabled_by_default
=== RUN   TestSecurityConfig/valid_security_config_loads_and_cascades
=== RUN   TestSecurityConfig/negative_fleet_budget_usd
=== RUN   TestSecurityConfig/negative_concurrency_cap
=== RUN   TestSecurityConfig/invalid_hostname_in_outbound_allowlist
=== RUN   TestSecurityConfig/invalid_hostname_in_step_security_override
--- PASS: TestSecurityConfig (0.00s)
    --- PASS: TestSecurityConfig/zero_config_has_security_enabled_by_default (0.00s)
    --- PASS: TestSecurityConfig/valid_security_config_loads_and_cascades (0.00s)
    --- PASS: TestSecurityConfig/negative_fleet_budget_usd (0.00s)
    --- PASS: TestSecurityConfig/negative_concurrency_cap (0.00s)
    --- PASS: TestSecurityConfig/invalid_hostname_in_outbound_allowlist (0.00s)
    --- PASS: TestSecurityConfig/invalid_hostname_in_step_security_override (0.00s)
PASS
ok  	jig/internal/workflow	0.191s
```

---

## Artifact: jig validate examples/feature.toml

**What it proves:** `examples/feature.toml` with the new `[defaults.security]`
block and a `[step.security]` per-step override on `security_scan` validates
cleanly.

**Why it matters:** `examples/feature.toml` is the kitchen-sink reference;
it must stay valid at every schema addition.

**Command:**

```bash
go run ./cmd/jig validate examples/feature.toml
```

**Result summary:** Exit 0, 15 steps validated.

```
ok: "feature" v1 — 15 step(s)
```

---

## Artifact: Full test suite

**What it proves:** No regressions in any package after the schema changes.

**Command:**

```bash
rtk proxy go test ./...
```

**Result summary:** All 9 tested packages pass.

```
?   	jig/cmd/jig	[no test files]
ok  	jig/internal/datastore	0.188s
ok  	jig/internal/engine	3.483s
?   	jig/internal/manifest	[no test files]
ok  	jig/internal/runner	0.700s
ok  	jig/internal/sentinel	0.811s
ok  	jig/internal/step	0.832s
ok  	jig/internal/transcript	0.515s
ok  	jig/internal/tui	0.990s
ok  	jig/internal/workflow	0.728s
```

---

## Artifact: New and updated documentation files

**What it proves:** All three required documentation artifacts were written
and are complete.

**Why it matters:** The task proof requires that docs are reviewed and complete
before the parent task is marked done.

| Artifact | Path | Status |
|----------|------|--------|
| Security monitoring end-to-end doc | `docs/security-monitoring.md` | Created |
| Workflow schema security section | `docs/workflow-schema.md` (appended) | Updated |
| ADR — out-of-band + raise-don't-kill | `docs/adr/0009-agent-security-monitoring.md` | Created |

---

## Reviewer Conclusion

The security configuration layer is complete: `SecurityConfig` and
`StepSecurity` parse and cascade correctly; all invalid values are caught at
load time; the kitchen-sink example validates; the full test suite is green;
and all three documentation artifacts have been written.

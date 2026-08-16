# Nested Go module for the ACP dependency

jig's Harness abstraction (spec 12) adds `coder/acp-go-sdk` to speak the Agent
Client Protocol to Claude. We isolated it in its own nested module,
`harness/acp` (module path `jig/harness/acp`), wired into the root module via a
`replace` directive, rather than adding it as a normal dependency of the root
`go.mod`. `coder/acp-go-sdk` is the most active Go ACP implementation
available but is still young and single-vendor-backed; nesting it means its
dependency tree — and the blast radius if it's abandoned or the protocol
churns — stays confined to `internal/harness`'s ACP-backend file(s) instead of
the whole jig module. This is the first nested-module layout in the repo and
sets the pattern (directory name, `jig/<path>` module path convention,
`replace`-during-development) for any future isolated, higher-risk
dependency.

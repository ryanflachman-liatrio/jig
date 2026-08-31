# SDD quality profiles

The SDD implementation module uses declared profiles instead of detecting a
project's tooling at runtime. Profiles are intentionally repository-specific:
using a profile for a different project requires an explicit replacement,
rather than silently guessing a package manager or linter.

| Profile | Tool/version | Command | Threshold/baseline | Applies when | Not applicable when |
|---|---|---|---|---|---|
| `go-build` | Go 1.25 (`mise.toml`) | `go build ./...` | Must exit zero | Go sources or `go.mod` changed | The selected task changes only docs/assets |
| `go-test` | Go 1.25 | `go test -coverprofile=.jig/coverage.out ./...` | Must exit zero; coverage artifact is exported immutably | Go sources or tests changed | The selected task changes only docs/assets |
| `go-format-lint` | `gofmt` and `go vet` from Go 1.25 | `test -z "$(gofmt -l .)" && go vet ./...` | No unformatted Go files and vet exits zero | Go sources changed | The selected task changes only docs/assets |
| `go-coverage` | `go tool cover` from Go 1.25 | `go tool cover -func="$JIG_INPUT_COVERAGE"` | Total coverage must be at least 70%; baseline is the task's exported profile | `go-test` produced a coverage artifact | `go-test` is inapplicable or failed/error |
| `dependency-review` | Go module tooling | Repository dependency analysis in the compliance module | No unreviewed critical finding | `go.mod` or `go.sum` changed | Neither module file changed |
| `api-contract-review` | Repository/API contract tools | Contract analysis in the validation module | No unresolved breaking contract finding | Public API surface changed | No public API surface exists |

Each `check` in `modules/implement_task.toml` embeds the profile command,
required executable list, applicability guard, typed finding protocol, and
bounded remediation route. The engine stores the test coverage export as an
immutable input for the coverage profile; no profile reaches into another
worktree.

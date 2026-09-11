# Slice 15 — Fair-share inline argument formatting

- **Slice ID:** `inline-arg-formatting`
- **Outcome:** A collapsed tool exchange previews its arguments as a compact
  `key=value, key=value` line that budgets width fairly across keys, so one long
  value never starves the keys after it.
- **Why this slice exists:** jig's collapsed row currently shows a single
  "primary" argument chosen by a per-kind switch, and a tool jig has no mapping
  for shows nothing useful. This is a small, self-contained improvement to the
  most-seen line in the panel.
- **Depends on:** Slice 02.

---

## omp reference

### Where it appears

`packages/coding-agent/src/tools/default-renderer.ts:66-72` — the generic
fallback card, collapsed:

```ts
const inlineBudget = Math.max(20, contentWidth - Bun.stringWidth(uiTheme.tree.last) - 2);
const preview = formatArgsInline(args, inlineBudget);
if (preview) {
    lines.push(` ${uiTheme.fg("dim", uiTheme.tree.last)} ${uiTheme.fg("dim", preview)}`);
}
```

Rendered:

```
 ⏳ MyTool
  └─ path="src/foo.ts", limit=50, mode="strict"
```

The preview hangs off a `└─` tree stub under the header, entirely dim.

### The algorithm

`tools/json-tree.ts:53-92`:

```ts
export function formatArgsInline(args: Record<string, unknown>, maxWidth: number): string {
    const keys: string[] = [];
    for (const key in args) {
        if (key in HIDDEN_ARG_KEYS) continue;
        keys.push(key);
    }
    let result = "";
    let width = 0;
    for (let i = 0; i < keys.length; i++) {
        const key = keys[i];
        const sep = width > 0 ? ", " : "";
        const current = width + (width > 0 ? 2 : 0);
        const cap = maxWidth - current - 1;              // 1 = width of "…"
        if (cap <= 0) return `${result}…`;

        // Reserve each still-pending key's minimal footprint (sep + name + `=` +
        // a short value) so a long value can't starve the keys that follow it.
        let tailReserve = 0;
        for (let j = i + 1; j < keys.length; j++) {
            tailReserve += 2 + Bun.stringWidth(keys[j]) + 1 + 4;
        }

        const pieceBudget = Math.min(cap, maxWidth - current - tailReserve);
        const valueMaxLen = Math.max(1, pieceBudget - Bun.stringWidth(key) - 3);
        const valueStr = formatScalar(value, valueMaxLen);
        const piece = `${key}=${valueStr}`;
        if (Bun.stringWidth(piece) > pieceBudget) {
            return `${result}${sep}${truncateToWidth(piece, cap)}`;
        }
        result += sep + piece;
        width = current + Bun.stringWidth(piece);
    }
    return result;
}
```

**The idea worth taking:** before spending width on key *i*, reserve the minimal
footprint of every key still to come (`", " + name + "=" + 4 chars`). A 2 KB
`content` value therefore cannot consume the whole line and hide the `path` that
follows it. The last key reserves nothing and fills whatever remains.

### Scalar formatting

`json-tree.ts:33-49`:

```ts
null      → "null"
undefined → "undefined"
boolean   → "true" / "false"
number    → String(value)
string    → `"${truncateToWidth(escaped, maxLen)}"`   // \n → \\n, \t → \\t
array     → `[${value.length} items]`
object    → `{${Object.keys(value).length} keys}`
```

Strings are quoted; newlines and tabs are escaped rather than dropped, so a
multi-line value stays on one line without corrupting layout. Containers are
summarized by *count*, not contents — which is what makes the line short.

`HIDDEN_ARG_KEYS` filters internal fields (`__partialJson`, an intent marker)
before formatting.

### Expanded counterpart

When the card is expanded, the same args render as a full JSON tree with `├─`/`└─`
connectors, depth-capped (2 collapsed / 6 expanded), line-capped (6 / 200), and
scalar-capped (60 / 2000 chars) — `default-renderer.ts:74-89`,
`json-tree.ts:8-14`.

---

## Current jig state

`internal/tui/monitor/monitor_tool_summary.go` picks **one** argument per known
tool kind (`:51-80`):

```go
case "read":
    return toolSummary("◈", "Read", shortFile(stringArg(args, "file_path", "path")))
case "bash":
    return toolSummary("$", "Run", stringArg(args, "command"))
case "grep":
    return toolSummary("⌕", "Search", stringArg(args, "pattern", "query"))
...
```

and for anything unrecognized falls back to `primaryToolArg(args)` — a heuristic
single-value pick.

The chosen value becomes `summary.preview` = `"· " + detail`, rendered after the
label (`items_view.go:92-94`), then clipped by the row's overall width.

### What is good

- The per-kind mapping is genuinely better than generic formatting **for known
  tools**. `Read · internal/tui/monitor/monitor.go` beats
  `path="internal/tui/monitor/monitor.go"`. **Do not replace it.**
- `shortFile` and `shortHost` already condense the common values.
- `sanitizeToolSummary` already runs over the result.

### What is missing

- **Unknown tools get one arbitrary value.** MCP tools, custom tools, and any
  backend-specific tool fall here. `primaryToolArg` picks a value with no
  guarantee it is the informative one.
- **No multi-argument preview.** A `grep` with a `pattern` and a `path` shows
  only the pattern; the scope is invisible until expanded.
- **No width budgeting.** `preview` is built without knowing the panel width and
  is clipped afterward, so a long value simply runs off rather than yielding
  space to a shorter, more informative sibling.
- **No container summarization.** An array argument renders as raw JSON or is
  skipped.

---

## In Scope

- An inline argument formatter in the monitor package implementing the
  fair-share budget: reserve pending keys' minimal footprints, format scalars
  compactly, summarize containers by count, escape newlines and tabs.
- Use it as the fallback when `summarizeActivity` has **no per-kind mapping**,
  replacing `primaryToolArg`.
- Use it to populate the `Meta` slot (slice 02) for known kinds that have useful
  secondary arguments — e.g. grep's `path`, `case`, `gitignore`.
- Filter internal/noise keys before formatting.
- Take the available width as a parameter so budgeting is real rather than
  post-hoc clipping.

## Out of Scope

- The expanded JSON tree. jig already renders expanded input via
  `prettyToolInput` → `fenceJSON` → Chroma (slice 05), which is at least as good
  as omp's hand-drawn tree.
- Replacing the per-kind mapping in `summarizeActivity` — it is better than
  generic formatting for the tools it covers.
- Argument previews for `bash`, whose single `command` argument is already the
  right preview.

## Functional Requirements

- **FR-15.1** A tool exchange with no per-kind mapping shall preview its
  arguments as `key=value` pairs joined by `", "`.
- **FR-15.2** The formatter shall accept an available width and shall not exceed
  it.
- **FR-15.3** Before allocating width to a key, the formatter shall reserve a
  minimal footprint for every remaining key.
- **FR-15.4** Strings shall be quoted with newlines and tabs escaped; arrays
  shall render as an item count; objects as a key count.
- **FR-15.5** When the budget is exhausted the formatter shall append an
  ellipsis rather than truncating mid-token without indication.
- **FR-15.6** Internal and noise keys shall be excluded.
- **FR-15.7** The preview shall be a single line under all inputs.
- **FR-15.8** An activity with no arguments shall produce an empty preview, not a
  placeholder.

## Technical and Repository Constraints

- `decodeToolArgs` (`monitor_tool_summary.go:99`) already unmarshals to
  `map[string]json.RawMessage`. **Go map iteration order is random** — omp relies
  on JS object insertion order, which jig cannot reproduce. Argument order must
  be made deterministic (sort keys, or sort with a small priority list putting
  `path`/`file_path`/`command`/`pattern` first). **Without this the preview will
  reorder between renders**, which is worse than the current behavior.
- All width math via `lipgloss.Width`.
- `sanitizeToolSummary` must still run over the output.
- The formatter needs the panel or card content width; `summarizeActivity`
  currently takes no width parameter, so the signature changes — coordinate with
  slice 02, which already restructures the summary's consumers.
- Values come from `json.RawMessage`; unmarshal each lazily to avoid decoding a
  large `content` value only to summarize it as a count.

## Security and Data Considerations

- Tool arguments are agent-controlled and may contain secrets that upstream
  redaction missed. The preview *shortens* exposure relative to the expanded
  view, so this is neutral-to-positive — but escaping (FR-15.4) matters: an
  argument containing `\x1b` must not reach the terminal raw. Verify
  `sanitizeToolSummary` strips escapes, and add it if not.
- Do not preview values for keys whose names suggest secrets (`token`, `key`,
  `password`, `secret`) — summarize them as `key=<redacted>`. omp has no such
  rule; jig should, since workflow tools may take credentials.

## Acceptance Evidence

- Table-driven tests: one long value plus three short keys, asserting all four
  keys appear; budget exhaustion appending an ellipsis; arrays and objects as
  counts; newline and tab escaping; empty args producing empty output.
- A test asserting the preview never exceeds the supplied width.
- A test asserting stable key ordering across repeated calls (the Go map hazard).
- A test asserting a secret-looking key is redacted.

## Inputs for the Child Spec

- **Go's random map iteration is the one thing that will silently break this.**
  Establish deterministic ordering before anything else.
- The per-kind mapping stays; this is the *fallback* and the *meta* source.
- The secret-key redaction rule is a jig addition with no omp precedent —
  justified because workflow tools can take credentials.
- Width must be a parameter, not a post-hoc clip; that is the substance of the
  slice.

## Open Questions

- **Q-15.1** Sorted keys or a priority list? Sorted is simple and predictable;
  a priority list surfaces the informative argument first. *Suggest a short
  priority list (`path`, `file_path`, `command`, `pattern`, `query`, `url`) then
  sorted remainder.*
- **Q-15.2** Should known kinds also gain a multi-key preview, or keep their
  single curated detail plus a meta list? *Suggest curated detail + meta; it
  reads better than a generic pair list.*
- **Q-15.3** What is the secret-key heuristic, and does it belong here or in
  `internal/runner`'s existing `redactSecrets`? *Redaction upstream is the better
  home if it can be extended; check before duplicating.*

# internal/datastore

This package owns every path under `.jig/`. Callers get paths only from
`Store`, `Run`, `Step`, and `ReviewRound` accessors; the layout tree in the
package doc (`datastore.go`) is the single source of truth for what lives where.

## Adding a file to the run layout

1. Add an accessor on the type that owns the file, built with that type's
   `path(...)` helper so a disabled location yields `""`.
2. Pick the file's write discipline:
   - **Whole-file durable state** (resumable or authoritative): write through
     `WriteFileAtomic` with `0o600`.
   - **Append-only history** (journal, transcript, findings): its owning
     package opens it `O_APPEND` and only ever appends.
   - **Derived step output** that a reset must discard: add it to
     `Step.ClearOutputs`.
3. Pick its read strictness and say which in the accessor's doc comment:
   - **Best-effort**: absent yields a zero value. Corruption also yields a
     zero value, plus an exported sentinel error so `jig doctor` can report
     it. `session.json` and `ErrSessionCorrupt` are the model.
   - **Authoritative**: every read failure is an error, because guessing
     would let resume rebuild state from a different source. Fan-out
     manifests are the model.
4. Add the file to the package doc tree.

Done when the accessor appears in both `TestPathAccessors` and
`TestDisabledLocationsYieldEmptyPaths`, and the package doc lists the file.

## The location contract

Each type's zero value means persistence is off. Every method follows one
shape, so new methods start the same way:

1. **Validation error first.** If `s.err` is set, mutations and reads
   return it.
2. **Disabled next.** If `s.dir == ""`, accessors return `""`, optional
   operations return nil, and reads return a zero value.
3. **Then act.** `Store.Create`, `Store.Resolve`, and `Store.Prune` are the
   required operations; they return `errDisabled` instead of succeeding as a
   no-op.

Every identifier joined into a path passes `validateSegment` before it
reaches the filesystem. Identifiers arrive from CLI input and generated
fan-out ids (which contain dots), so the rule is one safe path segment, not an
alphabet. `Store.Resolve` adds symlink and containment checks for CLI-supplied
run ids; reuse it for any new path that reads an operator-named run.

## Byte stability

- Fan-out manifests are digested and the digest is journaled. Encode them with
  `json.Marshal`: `MarshalIndent` reflows the embedded `json.RawMessage`
  items and invalidates every stored `ItemSHA256`.
- A manifest shape change bumps `FanOutManifestVersion` and adds explicit
  read support for the new version.
- `JournalKindRunFinished` is shared with the engine's journal encoder. Rename
  the kind there and here together.

## Dependency direction

This is a leaf package: it imports the standard library only. The engine,
sentinel, review, and runner packages import it. When datastore needs journal
data (retention does), decode a minimal local struct rather than importing
`engine`.

## Retention

Only runs whose journal contains a `run_finished` envelope are candidates. A
running or crashed run is always kept. `Prune` and `Prunable` share
`selectPrunable`, so a dry run reports exactly what a real prune removes.

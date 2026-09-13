# Synthetic persisted-transcript Monitor smoke

- Terminal: 80x24; initial transcript inner width: 44
- Fixture: temporary persisted transcript containing fabricated paths and output only.
- Sequence: `n`, `enter`, `enter`, `o`, `o`, `/third-content-only`, `F` + errors, resize narrow/wide, `N`/`n`.

## After `n` (group selected)

```text
    before grouped reads


▌ ✗ Read (3)
▌ ├─ one.go:1-2
▌ ├─ two.go:8-8
▌ └─ ✗ three.go:21-23


    after grouped reads
```

## After `enter` (local expansion)

```text
    before grouped reads


▌ ✗ Read (3)
▌ ├─ one.go:1-2
▌ ├─ two.go:8-8
▌ └─ ✗ three.go:21-23
▌ │  Input:
▌ │  │ {
▌ │  │   "file_path": "synthetic/one.go",
▌ │  │   "limit": 2,
▌ │  │   "offset": 1
▌ │  │ }
▌ │  Output:
▌ │  │ {
▌ │  │   "text": "synthetic member output"
▌ │  │ }
▌ │  Input:
▌ │  │ {
▌ │  │   "file_path": "synthetic/two.go",
▌ │  │   "limit": 1,
▌ │  │   "offset": 8
▌ │  │ }
▌ │  Output:
▌ │  │ {
▌ │  │   "text": "synthetic member output"
▌ │  │ }
▌    Input:
▌    │ {
▌    │   "file_path": "synthetic/three.go",
▌    │   "limit": 3,
▌    │   "offset": 21
▌    │ }
▌    Output:
▌    │ {
▌    │   "text": "third-output-only"
▌    │ }
▌    Content:
▌    │ third-content-only


    after grouped reads
```

## After collapse then `o` (global expansion)

```text
    before grouped reads


▌ ✗ Read (3)
▌ ├─ one.go:1-2
▌ ├─ two.go:8-8
▌ └─ ✗ three.go:21-23
▌ │  Input:
▌ │  │ {
▌ │  │   "file_path": "synthetic/one.go",
▌ │  │   "limit": 2,
▌ │  │   "offset": 1
▌ │  │ }
▌ │  Output:
▌ │  │ {
▌ │  │   "text": "synthetic member output"
▌ │  │ }
▌ │  Input:
▌ │  │ {
▌ │  │   "file_path": "synthetic/two.go",
▌ │  │   "limit": 1,
▌ │  │   "offset": 8
▌ │  │ }
▌ │  Output:
▌ │  │ {
▌ │  │   "text": "synthetic member output"
▌ │  │ }
▌    Input:
▌    │ {
▌    │   "file_path": "synthetic/three.go",
▌    │   "limit": 3,
▌    │   "offset": 21
▌    │ }
▌    Output:
▌    │ {
▌    │   "text": "third-output-only"
▌    │ }
▌    Content:
▌    │ third-content-only


    after grouped reads
```

## Search for third-member-only token

Visible items: 1; selected kind: 4; expanded: true

## Error filter

```text
▌ ✗ Read (3)
▌ ├─ one.go:1-2
▌ ├─ two.go:8-8
▌ └─ ✗ three.go:21-23
▌ │  Input:
▌ │  │ {
▌ │  │   "file_path": "synthetic/one.go",
▌ │  │   "limit": 2,
▌ │  │   "offset": 1
▌ │  │ }
▌ │  Output:
▌ │  │ {
▌ │  │   "text": "synthetic member output"
▌ │  │ }
▌ │  Input:
▌ │  │ {
▌ │  │   "file_path": "synthetic/two.go",
▌ │  │   "limit": 1,
▌ │  │   "offset": 8
▌ │  │ }
▌ │  Output:
▌ │  │ {
▌ │  │   "text": "synthetic member output"
▌ │  │ }
▌    Input:
▌    │ {
▌    │   "file_path": "synthetic/three.go",
▌    │   "limit": 3,
▌    │   "offset": 21
▌    │ }
▌    Output:
▌    │ {
▌    │   "text": "third-output-only"
▌    │ }
▌    Content:
▌    │ third-content-only
```

## Narrow resize

Terminal: 56x20; transcript inner width: 52

```text
▌ ✗ Read (3)
▌ ├─ one.go:1-2
▌ ├─ two.go:8-8
▌ └─ ✗ three.go:21-23
▌ │  Input:
▌ │  │ {
▌ │  │   "file_path": "synthetic/one.go",
▌ │  │   "limit": 2,
▌ │  │   "offset": 1
▌ │  │ }
▌ │  Output:
▌ │  │ {
▌ │  │   "text": "synthetic member output"
▌ │  │ }
▌ │  Input:
▌ │  │ {
▌ │  │   "file_path": "synthetic/two.go",
▌ │  │   "limit": 1,
▌ │  │   "offset": 8
▌ │  │ }
▌ │  Output:
▌ │  │ {
▌ │  │   "text": "synthetic member output"
▌ │  │ }
▌    Input:
▌    │ {
▌    │   "file_path": "synthetic/three.go",
▌    │   "limit": 3,
▌    │   "offset": 21
▌    │ }
▌    Output:
▌    │ {
▌    │   "text": "third-output-only"
▌    │ }
▌    Content:
▌    │ third-content-only
```

## Wide resize and `N`/`n` navigation

Terminal: 120x30; transcript inner width: 76; selected key: {anchor:{seq:2 block:0} kind:4}

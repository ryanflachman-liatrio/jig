# Phase 2 screenshots

Rendered from live `monitor.View()` ANSI frames (same states as the text proofs).

| Item | Shot | File |
|---|---|---|
| **2.1** | Wide embed (≥160): Steps \| `[REVIEW]` | [phase2_2_1_review_embed_wide.png](phase2_2_1_review_embed_wide.png) |
| **2.1** | Narrow (&lt;160): full-width `[REVIEW]` | [phase2_2_1_review_embed_narrow.png](phase2_2_1_review_embed_narrow.png) |
| **2.2** | Slim titles (`shortRun › Steps`, `step › [TRANSCRIPT]`) | [phase2_2_2_slim_titles.png](phase2_2_2_slim_titles.png) |
| **2.3** | Simple mode footer tag | [phase2_2_3_footer_simple.png](phase2_2_3_footer_simple.png) |
| **2.3** | Advanced mode footer (search/filters/paging) | [phase2_2_3_footer_advanced.png](phase2_2_3_footer_advanced.png) |

**2.4** is process-only (script + workflow); no UI chrome change beyond what 2.1–2.3 already show.

Regenerate:

```bash
go test ./internal/tui -run TestPhase2ScreenshotDump -count=1
python3 /tmp/render_phase2_shots.py   # or the helper in this PR’s CI notes
```

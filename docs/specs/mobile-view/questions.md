# Clarification questions: mobile-view

Please answer these questions in place. The recommended options keep the
feature local, automatic, and limited to presentation-layer behavior.

## 1. What execution surface does “mobile view” target?

- **Local narrow terminal only (recommended)** — adapt the existing Bubble Tea
  TUI when its terminal width is small; remote access, browser/mobile clients,
  and new persistence/security policy remain out of scope.
- Local terminal plus remote/mobile client support — requires a broader
  transport, interaction, and security design.

## 2. How should the TUI behave at and below its minimum usable width?

- **Automatic narrow layout with a single-panel fallback (recommended)** —
  derive explicit wide/threshold/narrow states from `WindowSizeMsg`, stack or
  hide secondary panels as needed, keep the app usable, and define a documented
  minimum width below which content is clipped or simplified.
- Keep the current multi-panel layout at every width — avoids new layout rules
  but does not deliver a meaningful mobile view.
- Refuse or exit below a minimum width — gives a predictable geometry but makes
  narrow-terminal use unavailable.

## 3. Which behavior must remain visible in the narrow layout?

- **Status, current step/output, navigation, approval actions, and security
  context (recommended)** — preserves the information and actions needed to
  operate and review a run while allowing secondary detail to collapse.
- Status and current output only — simpler layout, but navigation and review
  actions may require a separate mode or become inaccessible.
- Full parity with the wide layout — strongest information parity, but likely
  requires scrolling, paging, or more complex navigation rules.

## 4. Which screens are included in the first slice?

- **Every existing TUI screen that responds to terminal resizing
  (recommended)** — gives one consistent responsive contract and lets tests
  cover shared geometry/layout behavior.
- Monitor screen only — smallest implementation surface, but other screens may
  remain unusable at narrow widths.
- Monitor and run-review/approval screens only — focuses on active operation and
  human gates while leaving other screens unchanged.


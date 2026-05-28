# AstralOps Desktop UI Design Language

Last updated: 2026-05-28

This document is the desktop UI visual and interaction baseline for AstralOps. It records the design language decisions that should guide future React/Electron UI work.

## Product Feel

AstralOps is an operational AI workspace, not a marketing site. The desktop UI should feel quiet, dense, precise, and native enough to sit comfortably beside developer tools.

Prefer:

- Compact controls and readable information density.
- Clear hierarchy through spacing, weight, and subtle surfaces.
- Native platform behavior where it improves comfort, especially on macOS.
- Plain labels and lucide icons for affordances.
- Small, deliberate animation only for state transitions.

Avoid:

- Oversized controls, hero-scale typography, or large empty decorative surfaces.
- Card-inside-card layouts.
- Decorative gradients, orbs, bokeh, emoji, or ornamental symbols.
- One-off control sizing that makes nearby UI feel unrelated.
- In-app explanatory copy that teaches obvious interaction details.

## Scale

Use a compact desktop scale by default:

- Primary icon buttons: `32px` square.
- Small icon buttons inside rows: `24px` to `28px` square.
- Standard row height: `32px` for navigation rows, `44px` to `64px` for setting rows depending on description.
- Toolbar/button text: `12px` to `13px`.
- Navigation/session text: `13px` to `14px`.
- Transcript body text may use `15px` where reading comfort matters.
- Do not scale font size with viewport width.
- Letter spacing should remain normal.

Oversized controls should be treated as defects unless the component is a deliberate full-screen media or accessibility surface.

## Radius

Use a single default radius for most rectangular UI:

- Default control/card/popover radius: `8px`.
- Keep `rounded-full` only for true circles, such as toggles, avatars, and spinner rings.
- Avoid arbitrary large rounded rectangles such as `16px`, `18px`, or `22px`.
- Page sections should not be floating rounded cards. Use cards only for repeated items, modal/dialog surfaces, popovers, and framed controls.

## Surfaces

Use shared CSS tokens from `apps/desktop/src/styles.css`:

- `--ao-bg` for main content backgrounds.
- `--ao-panel` for sidebars, neutral panels, and quiet controls.
- `--ao-panel-strong` for hover/selected panel states.
- `--ao-panel-soft` for grouped settings/table-like rows.
- `--ao-border` and `--ao-border-strong` for separators.
- `--ao-hover` and `--ao-hover-strong` for hover states.
- `--ao-text`, `--ao-text-soft`, `--ao-muted`, `--ao-subtle` for text hierarchy.
- `--ao-blue`, `--ao-warning`, `--ao-danger`, `--ao-green` for semantic accents.

Do not introduce new beige, purple, slate, or heavy single-hue palettes without a product reason. New surfaces should first try the existing tokens.

## Shadows

Use shadows sparingly:

- Popovers need enough shadow to read as floating above content.
- Toolbar buttons and cards should usually use borders and subtle fills rather than heavy shadows.
- Media preview can use stronger image shadow because the image is the main object.
- Avoid stacking multiple shadowed cards.

Recommended:

- Popovers: `--ao-shadow-popover` or an equivalent subtle elevated shadow.
- Panels: `--ao-shadow-panel`.

## Platform Integration

macOS:

- Use Electron native vibrancy only on macOS.
- Sidebar surfaces should be transparent when native vibrancy is active so the system material can show through.
- Noninteractive titlebar/top spacer areas must be Electron drag regions.
- Buttons, navigation rows, and controls inside drag regions must explicitly be `[-webkit-app-region:no-drag]`.

Other platforms:

- Fall back to solid `--ao-panel` sidebar backgrounds.
- Do not depend on macOS-only visual effects for contrast or readability.

Theme:

- Follow the system theme by default.
- Dark mode must be driven by tokens, not one-off class overrides.
- Any new hardcoded light colors need an equivalent dark-mode behavior or should be replaced with tokens.

## Layout

Main app:

- Left sidebar is workspace/session navigation.
- Main surface is transcript and composer.
- Right panel is a secondary tool surface and should stay visually subordinate.
- Window chrome controls must stay compact and aligned with the 32px control scale.

Settings:

- Settings opens as a top-level settings mode, not a centered modal.
- Left side becomes settings navigation with a `返回应用` affordance.
- Right side is the settings content area.
- macOS settings sidebar should also use native vibrancy.
- Settings categories should be stable and concise: 通用, 外观, 会话, 工作区, 通知, 数据, 高级, 关于.
- Settings content should use grouped rows, not a grid of unrelated cards.
- Each setting row should be: title, short description, control.
- Copy should describe the setting, not explain how controls work.

Workspace creation:

- Creating a workspace should not ask for an agent.
- Agent is a session-level choice.
- Workspace creation copy should avoid internal implementation details.

## Controls

Buttons:

- Prefer icon-only buttons when the symbol is familiar.
- Use lucide icons.
- Text buttons are for clear commands.
- Default button height should be `32px` unless a form row requires more breathing room.

Toggles:

- Use compact switch dimensions, currently `40px x 24px`.
- Toggle state should be instantly visible and clickable.
- Do not use disabled-looking toggles for examples unless the example is genuinely disabled.

Segmented controls:

- Use `32px` height.
- Selected state should be a subtle raised fill, not a heavy brand color unless it is a primary mode.
- Segments must not resize the layout when selected.

Select/dropdown:

- Trigger and menu should have the same minimum width and aligned edges.
- Menu should sit slightly below the trigger, not touch it.
- Menu items need vertical spacing and clear selected state.
- Clicking outside the menu should close it.
- Menus must float above grouped rows and must not be clipped by parent containers.
- If select behavior becomes production-critical, prefer a real popover/select primitive with keyboard and focus handling rather than ad hoc positioning.

Modals:

- Use modals for interruptive workflows only, such as create workspace or pending approvals.
- Modal content should remain compact, with 8px radius and restrained shadow.
- Avoid large explanatory blocks unless the user must understand a risk before acting.

Media preview:

- Primary image/video should dominate the preview.
- Surrounding controls should be compact: 32px icon buttons, 8px radius.
- Zoom controls should be a small floating toolbar, not large circular controls.

## Transcript Behavior

Transcript is a timeline, not a debug dump.

User messages:

- Direct sends and running-time interjections both render as normal user messages in the timeline.
- Running-time user input should not disappear into hidden queue/control state.
- Interjections belong to the current long-running timeline, not a separate invisible next turn.

Agent activity:

- During a run, operation groups are collapsed by default.
- A collapsed operation row should show what is currently happening, such as reading a file or editing a file.
- When the agent speaks again, prior running activity should settle into a compact summary, such as edited files, commands run, searches, or reasoning blocks.
- Empty operation groups with no useful content should not expand into blank noise.
- `control.steer` and other meaningful user-visible flow markers are not noise by default; hide only truly empty or duplicated scaffolding.

Context usage:

- Context display must reflect the real current context window or native runtime usage.
- Do not calculate current context by summing stale historical transcript tokens after compaction.
- After compaction, old aggregate counts are not the active model window.

## Copy

- Use plain Chinese labels in the desktop UI.
- Do not expose implementation terms unless the target user needs them.
- Avoid mixed technical copy such as daemon/projection/internal protocol in normal UI.
- Error messages should be human-readable. Raw JSON error payloads should be parsed or formatted before display.
- Keyboard hints should be plain labels such as Enter, Cmd+Enter, or ESC.

## Interaction Details

- Clickable controls must visibly respond.
- Example controls in prototypes should still be interactive when possible, even if they only hold local state.
- Popovers and menus should close on outside click.
- Buttons inside Electron drag regions must be `no-drag`.
- Text must not overflow buttons, pills, cards, or compact rows.
- Hover/active states should not change layout dimensions.

## Verification Checklist

Before considering desktop UI work done:

- Run `npm run check -w apps/desktop`.
- Run `npm run build -w apps/desktop` when Tailwind classes or Electron/renderer integration changed.
- Manually inspect macOS light and dark mode when changing sidebar, chrome, settings, or global tokens.
- Check desktop and narrower window widths for text overflow and overlap.
- Verify popovers are not clipped by `overflow-hidden`.
- Verify drag regions still allow moving the window and controls remain clickable.
- Verify settings controls, dropdowns, and modal buttons can be interacted with.

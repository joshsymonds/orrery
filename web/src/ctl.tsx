import type { JSX } from "preact";

export type CtlIcon = "stop" | "play" | "send" | "more" | "edit" | "close";

// icons are the drawings of Ctl's icons, copied from the round-6 wireframe's icons.js. Each part is stroked in
// currentColor unless it is filled.
const icons: Record<CtlIcon, readonly { d: string; filled?: boolean }[]> = {
  stop: [{ d: "M5 4h6a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1z", filled: true }],
  play: [{ d: "M5 3.5l7 4.5-7 4.5z", filled: true }],
  send: [{ d: "M8 13V3M4 7l4-4 4 4" }],
  more: [
    {
      d: "M3.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM8 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM12.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2z",
      filled: true,
    },
  ],
  edit: [{ d: "M3 13l.5-2.5L11 3l2 2-7.5 7.5z" }],
  close: [{ d: "M4 4l8 8M12 4l-8 8" }],
};

// Ctl is a control inside a line: a small button with an optional icon, a bad or go tone, a key hint drawn at the desk,
// and icon-only form, where the label is its accessible name and its title and no text is drawn.
export function Ctl(props: {
  label: string; // the visible text, or the accessible name when only
  icon?: CtlIcon;
  tone?: "bad" | "go";
  kbd?: string; // a key hint drawn after the label as <kbd>, at the desk only (CSS hides it under 760 px)
  only?: boolean; // icon only: no visible text; aria-label and title are the label
  disabled?: boolean;
  tip?: string; // title; defaults to the label when only
  expanded?: boolean; // aria-expanded, for a control that opens something (More)
  onClick?: () => void;
}): JSX.Element {
  const { label, icon, tone, kbd, only, disabled, tip, expanded, onClick } = props;
  const classes = ["ctl", tone, only && "only"].filter(Boolean).join(" ");
  return (
    <button
      type="button"
      class={classes}
      aria-label={only ? label : undefined}
      title={tip ?? (only ? label : undefined)}
      disabled={disabled}
      aria-expanded={expanded}
      onClick={onClick}
    >
      {icon && (
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true">
          {icons[icon].map((part) =>
            part.filled ? (
              <path d={part.d} fill="currentColor" stroke="none" />
            ) : (
              <path d={part.d} />
            ),
          )}
        </svg>
      )}
      {!only && <span>{label}</span>}
      {kbd && <kbd>{kbd}</kbd>}
    </button>
  );
}

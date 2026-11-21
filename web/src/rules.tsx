import type { JSX } from "preact";

import { useFlash } from "./flash";
import { Icon } from "./glyphs";

// RuleKind is the kind of a rule, which its root carries as a class: a compaction, the since-you-looked mark, an
// interruption by the owner, or a change of local day.
type RuleKind = "compaction" | "new" | "interrupted" | "day";

// RuleProps are what a Rule draws: its kind, its label, and its detail when there is one. onToggle makes the rule a
// button that calls it on a click, with a chevron that shows whether open is set.
interface RuleProps {
  kind: RuleKind;
  label: string;
  detail?: string;
  onToggle?: () => void;
  open?: boolean;
}

// Rule draws a mark that spans the column between two hairlines: its label, then its detail when given. A rule with
// onToggle is a button, flashed on each click, and draws a chevron that points down while it is open.
export function Rule({ kind, label, detail, onToggle, open = false }: RuleProps): JSX.Element {
  const [flashing, flash] = useFlash();
  const classes = `rule ${kind}${open ? " open" : ""}${flashing ? " flash" : ""}`;
  const content = (
    <>
      <span class="label">{label}</span>
      {detail && <span class="detail">{detail}</span>}
      {onToggle && <Icon name={open ? "chevronDown" : "chevronRight"} />}
    </>
  );
  if (onToggle === undefined) {
    return (
      <div class={classes} data-shape="rule">
        {content}
      </div>
    );
  }
  return (
    <button
      type="button"
      class={classes}
      data-shape="rule"
      onClick={() => {
        flash();
        onToggle();
      }}
    >
      {content}
    </button>
  );
}

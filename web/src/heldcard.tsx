import type { ComponentChildren, JSX } from "preact";

import { designMark } from "./convorow";
import type { GaugeShare } from "./epicrow";
import { Gauge, Icon } from "./glyphs";
import type { HeldKind, HeldMove, HeldThing } from "./held";

// HeldCardProps are the held thing a desk's card draws, its title, the motion of the change that holds it, the press that
// folds the card back, the epic's gauge, and what the card holds. gauge is the gauge EpicRow draws for the epic, the share
// epicGaugeOf reads, and null for a design or a contract, whose marks need no counts.
export interface HeldCardProps {
  thing: HeldThing;
  title: string;
  move: HeldMove;
  gauge: GaugeShare | null;
  onFoldBack: () => void;
  children?: ComponentChildren;
}

// heldMark draws the mark a thing's row has in the inbox's tree: a design's mark, a contract's doc, and an epic's gauge, which
// EpicRow draws from the epic's share. An epic with no share draws the gauge with no counts.
function heldMark(kind: HeldKind, gauge: GaugeShare | null): JSX.Element {
  switch (kind) {
    case "design":
      return (
        <svg class="icon design-mark" viewBox="0 0 16 16" aria-hidden="true">
          <path d={designMark} />
        </svg>
      );
    case "contract":
      return <Icon name="doc" />;
    case "epic":
      return gauge === null ? <Gauge /> : <Gauge {...gauge} />;
  }
}

// HeldCard draws the held card at the desk: the held thing as a rounded card set into its conversation on the right, joined
// by necks to its inbox row, with Fold back at its left in place of Close. Its head begins with Fold back, on the spine that
// runs down its left edge, then the mark and the kind word of its row and its title. Its body is the children, and scrolls on
// its own. data-move names move, whose motion style.css draws. A press on Fold back calls onFoldBack.
export function HeldCard(props: HeldCardProps): JSX.Element {
  const { thing, title, move, gauge, onFoldBack, children } = props;
  return (
    <article class="held-card" data-kind={thing.kind} data-move={move === "" ? undefined : move} aria-label={title}>
      <span class="held-spine" aria-hidden="true" />
      <header class="held-head">
        <button type="button" class="fold-back" aria-label="Fold back" onClick={() => onFoldBack()}>
          <Icon name="foldBack" />
          <span>Fold back</span>
          <kbd>]</kbd>
        </button>
        <span class="held-mark" aria-hidden="true">
          {heldMark(thing.kind, gauge)}
        </span>
        <span class="held-kind">{thing.kind}</span>
        <span class="held-title">{title}</span>
      </header>
      <div class="held-body">{children}</div>
    </article>
  );
}

// HeldSheetProps are the held thing a narrow layout's sheet draws, its title, the motion of the change that holds it, the
// epic's gauge, the press that closes the sheet, and what the sheet holds.
export interface HeldSheetProps {
  thing: HeldThing;
  title: string;
  move: HeldMove;
  gauge: GaugeShare | null;
  onClose: () => void;
  children?: ComponentChildren;
}

// HeldSheet draws the held thing where the desk's card does not fit: a sheet over the conversation, closed by its Close
// press, with the thing's record in it. Its head holds the thing's mark, its kind word and its title, and at the right its X.
// Its spine and its foot are the bracket's ground (D5, D6). The sheet has no Fold back, as D15 leaves it unchanged. data-move
// names move, whose motion style.css draws.
export function HeldSheet(props: HeldSheetProps): JSX.Element {
  const { thing, title, move, gauge, onClose, children } = props;
  return (
    <section class="held-sheet" data-kind={thing.kind} data-move={move === "" ? undefined : move} aria-label={title}>
      <span class="held-sheet-spine" aria-hidden="true" />
      <header class="held-head">
        <span class="held-mark" aria-hidden="true">
          {heldMark(thing.kind, gauge)}
        </span>
        <span class="held-kind">{thing.kind}</span>
        <span class="held-title">{title}</span>
        <button type="button" class="held-close" aria-label="Close" onClick={() => onClose()}>
          <Icon name="close" />
        </button>
      </header>
      <div class="held-body">{children}</div>
      <span class="held-sheet-foot" aria-hidden="true" />
    </section>
  );
}

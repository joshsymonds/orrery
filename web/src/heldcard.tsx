import type { ComponentChildren, JSX } from "preact";

import type { HeldMove, HeldThing } from "./held";

// HeldCardProps are the held thing a desk's card draws, its title, the motion of the change that holds it, the press that
// folds the card back, and what the card holds.
export interface HeldCardProps {
  thing: HeldThing;
  title: string;
  move: HeldMove;
  onFoldBack: () => void;
  children?: ComponentChildren;
}

// HeldCard draws the held card at the desk: the held thing as a rounded card set into its conversation on the right, joined
// by necks to its inbox row, with Fold back at its left in place of Close. The card task will draw the card's motion for
// move. This stub draws the article only.
export function HeldCard(props: HeldCardProps): JSX.Element {
  const { thing, title, children } = props;
  return (
    <article class="held-card" data-kind={thing.kind} aria-label={title}>
      {children}
    </article>
  );
}

// HeldSheetProps are the held thing a narrow layout's sheet draws, its title, the motion of the change that holds it, the
// press that closes the sheet, and what the sheet holds.
export interface HeldSheetProps {
  thing: HeldThing;
  title: string;
  move: HeldMove;
  onClose: () => void;
  children?: ComponentChildren;
}

// HeldSheet draws the held thing where the desk's card does not fit: a sheet over the conversation, closed by its Close
// press, with the thing's record in it. The card task will draw the sheet's motion for move. This stub draws the section only.
export function HeldSheet(props: HeldSheetProps): JSX.Element {
  const { thing, title, children } = props;
  return (
    <section class="held-sheet" data-kind={thing.kind} aria-label={title}>
      {children}
    </section>
  );
}

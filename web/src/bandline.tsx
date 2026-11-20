import type { JSX } from "preact";

// BandLineProps are the thing a band line names, what it asks, and the act that answers it. open is whether the thing is
// already open in the panel on the right; onOpen opens it from the line.
export interface BandLineProps {
  name: string;
  asks: string;
  act: string;
  open: boolean;
  onOpen: () => void;
}

// BandLine draws one line of the conversation's band for a thing it steers that asks something: the thing's name in bold,
// what it asks, and the way in. Closed, the line is a button whose end reads its act, and a press opens the thing. Open in
// the panel, the line is a plain div that says open on the right and offers nothing, so the thing has no second way in.
export function BandLine({ name, asks, act, open, onOpen }: BandLineProps): JSX.Element {
  if (open) {
    return (
      <div class="subj attention here">
        <span class="what">
          <b>{name}</b>
        </span>
        <span class="st">{asks}</span>
        <span class="end">open on the right ›</span>
      </div>
    );
  }
  return (
    <button type="button" class="subj attention" onClick={onOpen}>
      <span class="what">
        <b>{name}</b>
      </span>
      <span class="st">{asks}</span>
      <span class="end">{act} ›</span>
    </button>
  );
}

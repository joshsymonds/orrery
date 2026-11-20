import type { JSX } from "preact";

// BandLine is one ask line of the band: the epic it is about, its slug, the text of the need, and the way in.
export interface BandLine {
  epic: string;
  slug: string;
  text: string;
  way: "Answer" | "Decide" | "Open";
}

// shownLines is how many ask lines the band draws; the rest are named in one more line.
const shownLines = 3;

// SubjectBand is what a conversation is for and what waits on it, drawn under its title row. The goal line draws first,
// when there is a goal, and carries the control that hides the header when onHide is given. Then the first three ask
// lines, each a button that opens its epic, and one line naming the slugs of any more. With no goal and no lines it
// draws nothing.
export function SubjectBand({
  goal,
  lines,
  onOpen,
  onHide,
}: {
  goal: string;
  lines: readonly BandLine[];
  onOpen: (epic: string) => void;
  onHide?: () => void;
}): JSX.Element | null {
  if (goal === "" && lines.length === 0) {
    return null;
  }
  const more = lines.slice(shownLines);
  return (
    <div class="subject" aria-label="what this conversation is for, and what waits on you">
      {goal !== "" && (
        <div class="subj goal">
          <span class="what">Goal</span>
          <span class="st">{goal}</span>
          {onHide !== undefined && (
            <button type="button" class="fold-head" aria-label="Hide the header" title="Hide the header" onClick={() => onHide()}>
              ⌃
            </button>
          )}
        </div>
      )}
      {lines.slice(0, shownLines).map((ask) => (
        <button
          key={ask.epic}
          type="button"
          class="subj attention"
          data-epic={ask.epic}
          onClick={() => onOpen(ask.epic)}
        >
          <span class="what">
            <b>{ask.slug}</b>
          </span>
          <span class="st">{ask.text}</span>
          <span class="end">{ask.way} ›</span>
        </button>
      ))}
      {more.length > 0 && (
        <div class="subj more">
          <span class="what">{`and ${more.length} more ask`}</span>
          <span class="st">{more.map((ask) => ask.slug).join(", ")}</span>
        </div>
      )}
    </div>
  );
}

// EpicMark draws an epic as a mark: a rim of one segment per requirement, in order and toned by the requirement's state,
// and a centre that says the epic's centre state. It reads the detail Epics.Get gives: detail.requirements and
// detail.centre. Its size is 32 in the inbox row and 24 elsewhere; the sizes and colours are in style.css.

import type { JSX } from "preact";

import type { EpicRequirement, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";

// EpicMarkSize is the size a mark draws at: 32 in the inbox row, and 24, the gauge's, elsewhere.
export type EpicMarkSize = 32 | 24;

// TONE_OF_STATE is the tone each requirement's segment is drawn in. A state it does not name is drawn as not reached.
const TONE_OF_STATE: Readonly<Record<string, string>> = {
  proven: "ok",
  in_flight: "run",
  held: "mine",
  failed: "bad",
  not_reached: "line.firm",
};

// toneOf is the tone a requirement in state is drawn in, not reached for a state it does not name.
function toneOf(state: string): string {
  return Object.hasOwn(TONE_OF_STATE, state) ? TONE_OF_STATE[state] : "line.firm";
}

// Centre is an epic's centre mark: the word its label reads, its tone, and what it draws at the ring's middle.
interface Centre {
  readonly word: string;
  readonly tone: string;
  readonly mark: JSX.Element;
}

// centreOf is the centre mark of an epic's centre state: the proven count while it runs, and a glyph for each other
// state. A centre it does not name has no centre mark.
function centreOf(centre: string, proven: number): Centre | null {
  switch (centre) {
    case "running":
      return { word: "running", tone: "live", mark: <text x="12" y="12">{proven}</text> };
    case "asks":
      return { word: "asks you a question", tone: "attention", mark: <text x="12" y="12">?</text> };
    case "paused":
      return { word: "paused", tone: "ink.3", mark: <path d="M10 8.5v7M14 8.5v7" /> };
    case "releasing":
      return { word: "releasing", tone: "engine", mark: <path d="M8.5 12h7M12.5 8.5l3.5 3.5-3.5 3.5" /> };
    case "shipped":
      return { word: "shipped", tone: "ok", mark: <path d="M8.6 12.3l2.3 2.3 4.4-4.9" /> };
    case "ended_short":
      return { word: "ended short", tone: "ink.3", mark: <path d="M9 12h6" /> };
    default:
      return null;
  }
}

// rimSegments draws one segment per requirement, in order: each takes an equal share of the ring less a gap, from the
// top, clockwise. A ring of one requirement is drawn whole.
function rimSegments(requirements: readonly EpicRequirement[]): JSX.Element[] {
  const share = 100 / requirements.length;
  const gap = requirements.length > 1 ? Math.min(3, share / 3) : 0;
  return requirements.map((requirement, i) => (
    <circle
      key={requirement.id}
      class="rim"
      data-id={requirement.id}
      data-state={requirement.state}
      data-tone={toneOf(requirement.state)}
      cx="12"
      cy="12"
      r="9"
      pathLength={100}
      stroke-dasharray={`${(share - gap).toFixed(2)} 100`}
      stroke-dashoffset={(-(i * share + gap / 2)).toFixed(2)}
      transform="rotate(-90 12 12)"
    />
  ));
}

// EpicMark draws an epic's mark at size. With no requirements it draws the dotted planned ring in place of the rim, so
// it is never empty; a centre it does not name draws no centre. Its label is the proven count and the centre's word.
export function EpicMark({
  detail,
  size = 24,
}: {
  detail: Pick<EpicsGetResponse, "requirements" | "centre">;
  size?: EpicMarkSize;
}): JSX.Element {
  const { requirements, centre } = detail;
  const proven = requirements.filter((requirement) => requirement.state === "proven").length;
  const drawn = centreOf(centre, proven);
  const count = requirements.length > 0 ? `${proven} of ${requirements.length} requirements proven` : "no requirements yet";
  const label = drawn === null ? count : `${count}, ${drawn.word}`;
  return (
    <svg class="epicmark" data-size={size} viewBox="0 0 24 24" role="img" aria-label={label}>
      <title>{label}</title>
      {requirements.length === 0 ? (
        <circle class="planned" cx="12" cy="12" r="9" stroke-dasharray="2 3" />
      ) : (
        rimSegments(requirements)
      )}
      {drawn !== null && (
        <g class="centre" data-tone={drawn.tone}>
          {drawn.mark}
        </g>
      )}
    </svg>
  );
}

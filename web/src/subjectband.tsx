import type { JSX } from "preact";

// BandLine is one ask line of the band: the epic it is about, its slug, the text of the need, and the way in.
export interface BandLine {
  epic: string;
  slug: string;
  text: string;
  way: "Answer" | "Decide" | "Open";
}

// ContractBandLine is one contract that waits on the owner: its id, its slug, and the text of what is left to settle.
export interface ContractBandLine {
  id: string;
  slug: string;
  text: string;
}

// BandAsk is one line of the band before it is drawn: a contract that waits on the owner, or an epic's ask.
type BandAsk = { kind: "contract"; contract: ContractBandLine } | { kind: "epic"; epic: BandLine };

// shownLines is how many ask lines the band draws; the rest are named in one more line.
const shownLines = 3;

// docIcon is the doc glyph a contract line draws, round 6's.
const docIcon = "M4 2h5l3 3v9H4zM9 2v3h3M6 8h4M6 11h4";

// askName is the name an ask takes in the more line: a contract's slug and "contract", an epic's slug.
function askName(ask: BandAsk): string {
  return ask.kind === "contract" ? `${ask.contract.slug} contract` : ask.epic.slug;
}

// SubjectBand is what a conversation is for and what waits on it, drawn under its title row. The goal line draws first,
// when there is a goal, and carries the control that hides the header when onHide is given. Then the first three ask
// lines: the contracts that wait on the owner, in the order given, then the epics' asks. A contract line is a button that
// opens it on the right, except the contract open in the panel (hereContract), which says it is open on the right. An
// epic line is a button that opens its epic, except the line of the epic open in the panel (here), which says so too.
// One more line names the rest by their slugs, a contract by its slug and "contract". With no goal, no contracts and no
// lines it draws nothing.
export function SubjectBand({
  goal,
  lines,
  contracts = [],
  onOpen,
  onOpenContract,
  onHide,
  here,
  hereContract,
}: {
  goal: string;
  lines: readonly BandLine[];
  contracts?: readonly ContractBandLine[];
  onOpen: (epic: string) => void;
  onOpenContract?: (id: string) => void;
  onHide?: () => void;
  // here is the epic open in the panel beside the conversation: its line says so instead of offering the way in.
  here?: string;
  // hereContract is the contract open in the panel beside the conversation: its line says so instead of offering to open it.
  hereContract?: string;
}): JSX.Element | null {
  if (goal === "" && lines.length === 0 && contracts.length === 0) {
    return null;
  }
  const asks: BandAsk[] = [
    ...contracts.map((contract): BandAsk => ({ kind: "contract", contract })),
    ...lines.map((epic): BandAsk => ({ kind: "epic", epic })),
  ];
  const more = asks.slice(shownLines);
  return (
    <div class="subject" aria-label="what this conversation is for, and what waits on you">
      {goal !== "" && (
        <div class="subj goal">
          <span class="what">Goal</span>
          <span class="st">{goal}</span>
          {onHide !== undefined && foldHead(onHide)}
        </div>
      )}
      {asks.slice(0, shownLines).map((ask) =>
        ask.kind === "contract" ? (
          <ContractLine
            key={`contract:${ask.contract.id}`}
            contract={ask.contract}
            here={ask.contract.id === hereContract}
            onOpen={onOpenContract}
          />
        ) : (
          <EpicLine key={`epic:${ask.epic.epic}`} ask={ask.epic} here={ask.epic.epic === here} onOpen={onOpen} />
        ),
      )}
      {more.length > 0 && (
        <div class="subj more">
          <span class="what">{`and ${more.length} more ask`}</span>
          <span class="st">{more.map(askName).join(", ")}</span>
        </div>
      )}
    </div>
  );
}

// ContractLine draws a contract that waits on the owner: a button that opens it on the right, or, when it is the contract
// open in the panel, the line saying so, with no way in.
function ContractLine({
  contract,
  here,
  onOpen,
}: {
  contract: ContractBandLine;
  here: boolean;
  onOpen?: (id: string) => void;
}): JSX.Element {
  const name = <b>{`${contract.slug} contract`}</b>;
  if (here) {
    return (
      <div class="subj attention here" data-contract={contract.id}>
        <span class="what">{name}</span>
        <span class="st">{contract.text}</span>
        <span class="end">open on the right ›</span>
      </div>
    );
  }
  return (
    <button type="button" class="subj attention" data-contract={contract.id} onClick={() => onOpen?.(contract.id)}>
      <svg class="icon" viewBox="0 0 16 16" aria-hidden="true" fill="none" stroke="currentColor">
        <path d={docIcon} />
      </svg>
      <span class="what">{name}</span>
      <span class="st">{contract.text}</span>
      <span class="end">Read and decide ›</span>
    </button>
  );
}

// EpicLine draws an epic's ask: a button that opens the epic, or, when it is the epic open in the panel, the line saying so.
function EpicLine({ ask, here, onOpen }: { ask: BandLine; here: boolean; onOpen: (epic: string) => void }): JSX.Element {
  if (here) {
    return (
      <div class="subj attention here" data-epic={ask.epic}>
        <span class="what">
          <b>{ask.slug}</b>
        </span>
        <span class="st">{ask.text}</span>
        <span class="end">open on the right ›</span>
      </div>
    );
  }
  return (
    <button type="button" class="subj attention" data-epic={ask.epic} onClick={() => onOpen(ask.epic)}>
      <span class="what">
        <b>{ask.slug}</b>
      </span>
      <span class="st">{ask.text}</span>
      <span class="end">{ask.way} ›</span>
    </button>
  );
}

// foldHead is the control that hides the header: on the band's goal line while the conversation's strip draws no tiles,
// and at the strip's end once it does, as round 6 places it.
export function foldHead(onHide: () => void): JSX.Element {
  return (
    <button type="button" class="fold-head" aria-label="Hide the header" title="Hide the header" onClick={() => onHide()}>
      ⌃
    </button>
  );
}

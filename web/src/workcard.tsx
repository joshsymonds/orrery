import type { JSX } from "preact";

import { nowSentence } from "./blocker";
import { type GaugeShare, epicRowWords } from "./epicrow";
import type { Contract } from "./gen/orrery/v1/contracts_pb";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { Gauge, Icon } from "./glyphs";
import { type Need, epicNeeds } from "./needs";
import { wedgesOf } from "./phases";
import { trackPart, trackParts } from "./status";
import type { EpicNode, Hung } from "./tree";

// The contract state that waits to be read and accepted.
const proposedState = "proposed";

// A stage of a card's track: a part passed, the part the item is in now, or a part still ahead.
export type TrackStage = "done" | "now" | "ahead";

// CardModel is one card's words: the item key its card, act and Open open, its slug, its gauge (null for a contract), its
// tone (attention, live, or none), its state (the pill's words), its track (one stage per trackParts part), its now
// sentence, its asks (empty when nothing is asked), the button that answers the ask (empty when none), and its count.
export interface CardModel {
  key: string;
  slug: string;
  gauge: GaugeShare | null;
  tone: string;
  state: string;
  track: TrackStage[];
  now: string;
  asks: string;
  act: string;
  count: string;
}

// workCardOf reads the card of a hung thing at now, in unix seconds: a contract waiting to be read and accepted, whether
// it hangs alone or under its epic, or else the card of the epic.
export function workCardOf(hung: Hung, now: number): CardModel {
  if (hung.kind === "contract") {
    return contractCard(hung.key, hung.contract);
  }
  if (hung.contract?.state === proposedState) {
    return contractCard(hung.key, hung.contract);
  }
  return epicCard(hung, now);
}

// contractCard reads a contract waiting: its slug, its care, the contract's title, the decisions it leaves or that it
// waits to be accepted, its version, and the Read and decide that opens it.
function contractCard(key: string, contract: Contract): CardModel {
  const decisions = contract.openDecisions;
  const waiting = contract.state === proposedState;
  return {
    key,
    slug: contract.id.slice(contract.id.lastIndexOf("/") + 1),
    gauge: null,
    tone: waiting ? "attention" : "",
    state: contract.care === "" ? contract.state : `${contract.care} care`,
    track: stagesOf(0),
    now: contract.title,
    asks: !waiting
      ? ""
      : decisions > 0n
        ? `${decisions} ${decisions === 1n ? "decision" : "decisions"} left to answer`
        : "waits for you to accept",
    act: waiting ? "Read and decide" : "",
    count: `v${contract.version}`,
  };
}

// epicCard reads an epic's card: its words from the row, its track from its phase, its now sentence, and its need. An
// epic with a need of its own, a live epic that is not archived, asks: its question under Answer, or else its blocker
// under Decide. A live epic with no need is building; an ended epic has no tone.
function epicCard(node: EpicNode, now: number): CardModel {
  const listed = node.listed;
  const detail = node.block.epic.detail;
  const words = epicRowWords(node, now);
  const need = detail === undefined || listed.archived ? undefined : epicNeeds([listed], new Map([[listed.id, detail]]))[0];
  const ask = askOf(need, detail, words.blocker);
  const part = detail === undefined ? undefined : trackPart(detail);
  return {
    key: node.key,
    slug: listed.slug,
    gauge: words.gauge,
    tone: need !== undefined ? "attention" : listed.live && !listed.archived ? "live" : "",
    state: words.words,
    track: stagesOf(part === undefined ? 1 : trackParts.indexOf(part), part !== undefined),
    now: detail === undefined ? "" : nowSentence(wedgesOf(detail)),
    asks: ask.asks,
    act: ask.act,
    count: words.count,
  };
}

// askOf is what an epic's need asks its owner: the text of its open question under Answer, or else the epic's blocker
// under Decide. An epic with no need asks nothing.
function askOf(
  need: Need | undefined,
  detail: EpicsGetResponse | undefined,
  blocker: string,
): { asks: string; act: string } {
  if (need === undefined) {
    return { asks: "", act: "" };
  }
  if (need.kind === "question") {
    return { asks: detail?.questions.find((question) => question.id === need.question)?.text ?? "", act: "Answer" };
  }
  return { asks: blocker, act: "Decide" };
}

// stagesOf draws the track's stages with the stage at current now, the stages before it passed, and the rest ahead.
// With no part now, the stage at current is ahead too: an epic whose phase the track does not name has its contract
// passed and nothing now.
function stagesOf(current: number, hasNow = true): TrackStage[] {
  return trackParts.map((_, index) =>
    index < current ? "done" : index === current && hasNow ? "now" : "ahead",
  );
}

// WorkCard draws one card of a project's page: its gauge, slug and pill, its track, its now sentence, its ask with the
// button that answers it, and its count with Open. The card, its act button and Open each call onOpen with the card's key.
export function WorkCard({ card, onOpen }: { card: CardModel; onOpen: (key: string) => void }): JSX.Element {
  const pillTone = card.tone === "attention" ? "waiting" : card.tone === "live" ? "working" : "idle";
  return (
    <div
      class={card.tone === "" ? "wcard" : `wcard ${card.tone}`}
      role="link"
      data-key={card.key}
      onClick={() => onOpen(card.key)}
    >
      <div class="top">
        {card.gauge !== null && <Gauge {...card.gauge} />}
        <span class="slug">{card.slug}</span>
        <span class={`end pill tone-${pillTone}`}>{card.state}</span>
      </div>
      <div class="mtrack" title="contract, planning, building, releasing, report">
        {card.track.map((stage) => (
          <i class={stage} />
        ))}
      </div>
      {card.now !== "" && <p class="now">{card.now}</p>}
      {card.asks !== "" && (
        <div class="asks">
          <Icon name="question" />
          <span>{card.asks}</span>
          <button type="button" class="btn primary act">
            {card.act}
          </button>
        </div>
      )}
      <div class="foot">
        <span>{card.count}</span>
        <button type="button" class="link end">
          Open
        </button>
      </div>
    </div>
  );
}

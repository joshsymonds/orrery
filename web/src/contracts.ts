// The rules and words a contract shares across the contract panel, the transcript block and the subject band: what is
// left to settle, when Accept is offered, the record line, the state pill, the rating tones and the About message.

import type {
  ContractDecision,
  ContractStateChange,
  ContractsGetResponse,
  ContractVersion,
} from "./gen/orrery/v1/contracts_pb";
import { clock } from "./reader";

// RowAbout is the row a dock About concerns: its id and the text it shows.
export interface RowAbout {
  id: string;
  text: string;
}

// Left is what a contract still asks of its owner: its open decisions, and whether its irreversible section is unread.
export interface Left {
  decisions: number;
  unread: boolean;
}

// contractSlug is a contract's id past its last slash: "tidepool/tide-alerts" is "tide-alerts".
export function contractSlug(id: string): string {
  return id.slice(id.lastIndexOf("/") + 1);
}

// newestVersion is the last of a Get's versions, which come oldest first.
export function newestVersion(get: ContractsGetResponse): ContractVersion | undefined {
  return get.versions.at(-1);
}

// openDecisions counts the decisions that have no answer yet.
export function openDecisions(decisions: readonly ContractDecision[]): number {
  return decisions.filter((decision) => decision.answer === "").length;
}

// concernedRows is the row of every open decision that names one.
export function concernedRows(decisions: readonly ContractDecision[]): Set<string> {
  return new Set(
    decisions.filter((decision) => decision.answer === "" && decision.row !== "").map((decision) => decision.row),
  );
}

// readKey is the key a viewer's read of a contract's irreversible section is stored under.
export function readKey(id: string, version: bigint): string {
  return `${id}@${version}`;
}

// leftOf is what a contract still asks: its open decisions, and an irreversible section unread at severe care.
export function leftOf(decisions: number, care: string, shown: boolean): Left {
  return { decisions, unread: care === "severe" && !shown };
}

// leftItems words each thing left to settle, decisions first.
export function leftItems(left: Left): string[] {
  const items: string[] = [];
  if (left.decisions > 0) {
    items.push(left.decisions === 1 ? "1 decision is yours" : `${left.decisions} decisions are yours`);
  }
  if (left.unread) {
    items.push("read “can’t be undone” before Accept");
  }
  return items;
}

// bandText is the band line's words for what is left to settle.
export function bandText(left: Left): string {
  const items = leftItems(left);
  return items.length > 0 ? items.join("; ") : "nothing left to settle";
}

// footText is the foot line's sentence for what is left to settle.
export function footText(left: Left): string {
  const items = leftItems(left);
  if (items.length === 0) {
    return "Nothing is left to settle.";
  }
  const things = items.length === 1 ? "1 thing left" : `${items.length} things left`;
  return `${things}: ${items.join("; ")}.`;
}

// acceptable is whether Accept is offered for the version drawn: the contract is proposed, the drawn version is the
// newest and has no open decision, a severe contract's irreversible section was read, and nothing is being sent.
export function acceptable(
  get: ContractsGetResponse,
  drawn: bigint,
  read: ReadonlySet<string>,
  sending: boolean,
): boolean {
  const contract = get.contract;
  const newest = newestVersion(get);
  if (contract === undefined || newest === undefined) {
    return false;
  }
  if (sending || contract.state !== "proposed" || newest.version !== drawn) {
    return false;
  }
  if (openDecisions(newest.decisions) > 0) {
    return false;
  }
  return contract.care !== "severe" || read.has(readKey(contract.id, drawn));
}

// AcceptStep is what the Accept press does after its re-read: send the Accept, or redraw the newer answer instead.
export type AcceptStep = { kind: "send" } | { kind: "redraw"; get: ContractsGetResponse };

// acceptStep sends the Accept only while the contract is still proposed at the version drawn.
export function acceptStep(drawn: bigint, reread: ContractsGetResponse): AcceptStep {
  const contract = reread.contract;
  if (contract !== undefined && contract.state === "proposed" && contract.version === drawn) {
    return { kind: "send" };
  }
  return { kind: "redraw", get: reread };
}

// lastChange is the last state change to the state given; the states come oldest first.
function lastChange(states: readonly ContractStateChange[], state: string): ContractStateChange | undefined {
  return states.filter((change) => change.state === state).at(-1);
}

// recordLine is the decision line's record words: when the contract was proposed, or the last accept or set-aside.
export function recordLine(get: ContractsGetResponse): string {
  const contract = get.contract;
  const newest = newestVersion(get);
  if (contract === undefined || newest === undefined) {
    return "";
  }
  if (contract.state === "proposed") {
    if (get.versions.length === 1) {
      return `v1 proposed ${clock(newest.time)}`;
    }
    return `v${newest.version} proposed ${clock(newest.time)} · first written ${clock(get.versions[0].time)}`;
  }
  const change =
    contract.state === "accepted" || contract.state === "set_aside"
      ? lastChange(get.states, contract.state)
      : undefined;
  if (change === undefined) {
    return `v${newest.version} ${contract.state === "set_aside" ? "set aside" : contract.state}`;
  }
  const who = change.by === "owner" ? "you" : "the session";
  if (contract.state === "set_aside") {
    return `v${change.version} set aside by ${who} ${clock(change.time)} with “${change.words}”`;
  }
  const withWords = change.words === "" ? "" : ` with “${change.words}”`;
  return `v${change.version} accepted by ${who} ${clock(change.time)}${withWords}`;
}

// statePill is the words of a contract's state.
export function statePill(state: string): string {
  switch (state) {
    case "proposed":
      return "proposed · waits for you";
    case "accepted":
      return "accepted";
    case "set_aside":
      return "set aside";
    default:
      return state;
  }
}

// ratingTone is the tone a rating takes: limited is grey, serious yellow and severe red.
export function ratingTone(rating: string): "" | "live" | "bad" {
  if (rating === "severe") {
    return "bad";
  }
  if (rating === "serious") {
    return "live";
  }
  return "";
}

// aboutMessage is the message sent with a row's About: the row's id and text, then the draft.
export function aboutMessage(about: RowAbout, draft: string): string {
  return `About ${about.id}: ${about.text}\n\n${draft}`;
}

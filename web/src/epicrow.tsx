import type { JSX } from "preact";

import { blockerLine } from "./epicpage";
import type { Contract } from "./gen/orrery/v1/contracts_pb";
import type { Epic, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { type EpicBlock, type EpicItem, epicLine, type StepItem } from "./epics";
import { Gauge, type GaugeInner, Icon, StateGlyph } from "./glyphs";
import { epicNeeds } from "./needs";
import type { CellKind } from "./orbitcells";
import { type PipTone, Pips } from "./pips";
import { durationText } from "./phases";
import { blockerOf, trackPart } from "./status";
import type { EpicNode, Hung } from "./tree";

// The outcome an ended epic reads as shipped, the contract state that waits to be read and accepted, and the ask it
// waits on.
const releasedOutcome = "released";
const proposedState = "proposed";
const waitsOnAccept = "waits to be read and accepted";
// The state of a release step that did not pass.
const failedStep = "failed";
// pipTone is the pip a step's ring cell draws: the ring it works toward runs, a held one is held, a failed one failed,
// a ring with evidence passed, and a ring not reached none.
const pipTone: Record<CellKind, PipTone> = { now: "run", held: "mine", errored: "bad", passed: "ok", track: "none" };

// GaugeShare is an epic's gauge: the done share of its tasks out of the total, its tone, and the mark at its centre.
export interface GaugeShare {
  done: number;
  total: number;
  tone: string;
  inner?: GaugeInner;
}

// EpicRowWords is what a row says in words: its count, its state with the time it has been in that state, its blocker
// (empty when nothing blocks it), and its gauge, which a contract's row does not draw.
export interface EpicRowWords {
  count: string;
  words: string;
  blocker: string;
  gauge: GaugeShare | null;
}

// contractOf is the contract a row draws as a contract: a contract item's own, or an epic's proposed contract. An epic
// whose contract is not proposed draws as an epic, and its contract is not drawn.
function contractOf(hung: Hung): Contract | null {
  if (hung.kind === "contract") {
    return hung.contract;
  }
  return hung.contract?.state === proposedState ? hung.contract : null;
}

// epicRowWords reads what a row says at now, in unix seconds. A contract reads its version, its state with its age,
// and, while it is proposed, the ask it waits on. An epic reads its count, its state word with the time since its phase
// began, its blocker, and its gauge.
export function epicRowWords(hung: Hung, now: number): EpicRowWords {
  const contract = contractOf(hung);
  if (contract !== null) {
    return {
      count: `v${contract.version}`,
      words: `contract · ${contract.state} · ${sinceText(Number(contract.changed), now, false)}`,
      blocker: contract.state === proposedState ? waitsOnAccept : "",
      gauge: null,
    };
  }
  if (hung.kind !== "epic") {
    throw new Error("a contract item always reads as a contract");
  }
  return epicWords(hung, now);
}

// sinceText is the time since an epic's phase began, or a contract's change: "49 min" while the epic is live, and
// "49 min ago" once it has ended. Something with no time reads none, and a clock behind the server's stamp reads zero.
function sinceText(since: number, now: number, live: boolean): string {
  if (since <= 0) {
    return "";
  }
  const elapsed = durationText(Math.max(0, now - since));
  return live ? elapsed : `${elapsed} ago`;
}

// epicWords reads an epic's row: its count from its line, its state word with the time since its phase began, its
// blocker and its gauge. An epic with no state word, such as one with no detail, reads no words.
export function epicWords(node: EpicNode, now: number): EpicRowWords {
  const item = node.block.epic;
  const listed = node.listed;
  const state = stateWordOf(node);
  const blocker = blockerWords(node, now);
  const released = !listed.live && listed.outcome === releasedOutcome;
  const timed = sinceText(item.phaseAt, now, listed.live);
  return {
    count: epicLine(item, now).count,
    words: state === "" ? "" : [state, timed].filter((part) => part !== "").join(" · "),
    blocker,
    gauge: gaugeOf(item, state, blocker, listed.live, released),
  };
}

// stateWordOf is an epic's state word: shipped or ended short once it has ended; paused by you or on a usage limit,
// then parked; and then its track part from its detail. An epic with no detail has none.
function stateWordOf(node: EpicNode): string {
  const listed = node.listed;
  if (!listed.live) {
    return listed.outcome === releasedOutcome ? "shipped" : "ended short";
  }
  if (listed.paused) {
    return listed.pauseSource === "limit" ? "paused on a usage limit" : "paused by you";
  }
  if (listed.parked !== "") {
    return "parked";
  }
  const detail = node.block.epic.detail;
  return detail === undefined ? "" : (trackPart(detail) ?? "");
}

// blockerWords is the first thing an epic waits on, as its row says it under the epic's name: a question with its wait,
// a failed release step with its reason, a park's reason, or any other need's text without the slug the row already
// draws. An ended epic waits on nothing.
function blockerWords(node: EpicNode, now: number): string {
  const detail = node.block.epic.detail;
  const details = new Map<string, EpicsGetResponse>();
  if (detail !== undefined) {
    details.set(node.listed.id, detail);
  }
  const first = epicNeeds([node.listed], details)[0];
  if (first === undefined) {
    return "";
  }
  if (first.kind === "question" && detail !== undefined) {
    return blockerLine(blockerOf(detail), now) ?? "";
  }
  const failed = detail?.releaseSteps.find((proof) => proof.state === failedStep);
  if (first.kind === "release" && failed !== undefined) {
    return `Release step ${failed.position} failed: ${failed.reason}`;
  }
  if (first.kind === "park" && detail !== undefined) {
    return detail.parked;
  }
  const named = `${node.listed.slug}: `;
  return first.text.startsWith(named) ? first.text.slice(named.length) : first.text;
}

// gaugeOf draws an epic's gauge: an ended epic's check when it shipped and its dash otherwise; a blocked epic's dot in
// attention; a paused epic in no tone; a releasing epic in engine; and any other in live.
function gaugeOf(item: EpicItem, state: string, blocker: string, live: boolean, released: boolean): GaugeShare {
  const { done, total } = item;
  if (!live) {
    return released ? { done, total, tone: "ok", inner: "check" } : { done, total, tone: "", inner: "dash" };
  }
  if (blocker !== "") {
    return { done, total, tone: "attention", inner: "dot" };
  }
  if (state.startsWith("paused")) {
    return { done, total, tone: "" };
  }
  return { done, total, tone: state === "releasing" ? "engine" : "live" };
}

// stepState is the glyph state of a running step: limited, held, or working.
function stepState(step: StepItem): string {
  if (step.step.limited) {
    return "limited";
  }
  return step.held ? "held" : "working";
}

// EpicRow draws a row of the inbox tree: an epic hung under its conversation, with its running steps under it, or a
// contract hung there, with the contract's mark. Its gauge, name, count, words and blocker are epicRowWords'. An epic's
// row draws each running step, then a line for the steps past the shown ones and the block's tail. A press on the row
// calls onSelect with its key, and a press on a step with the step's key. currentStep is the key of the step the cursor
// is on. The row draws no command.
export function EpicRow({
  hung,
  now,
  current,
  currentStep,
  onSelect,
}: {
  hung: Hung;
  now: number;
  current: boolean;
  currentStep?: string;
  onSelect: (key: string) => void;
}): JSX.Element {
  const words = epicRowWords(hung, now);
  const contract = contractOf(hung);
  const epic = hung.kind === "epic" && contract === null ? hung : null;
  const ended = hung.kind === "epic" && !hung.listed.live;
  const name = contract !== null ? contract.id.slice(contract.id.indexOf("/") + 1) : hung.kind === "epic" ? hung.listed.slug : "";
  const classes = ["row", "epic"];
  if (contract !== null) classes.push("contract");
  if (ended) classes.push("ended");
  if (current) classes.push("cur");
  return (
    <>
      <div
        class={classes.join(" ")}
        role="link"
        data-key={hung.key}
        aria-current={current ? "true" : undefined}
        onClick={() => onSelect(hung.key)}
      >
        {contract !== null ? (
          <span class="cmark">
            <Icon name="doc" />
          </span>
        ) : (
          words.gauge !== null && <Gauge {...words.gauge} />
        )}
        <span class="title">{name}</span>
        {contract !== null && <span class="age">{words.count}</span>}
        <span class="l2">
          {contract !== null ? words.words : [words.words, words.count].filter((part) => part !== "").join(" · ")}
        </span>
        {words.blocker !== "" && (
          <span class="l3">
            <Icon name="question" />
            {words.blocker}
          </span>
        )}
      </div>
      {epic !== null && <EpicSteps block={epic.block} currentStep={currentStep} onSelect={onSelect} />}
    </>
  );
}

// EpicSteps draws a block's running steps, each on a row of its own, and the line for the steps past the shown ones and
// the block's tail.
function EpicSteps({
  block,
  currentStep,
  onSelect,
}: {
  block: EpicBlock;
  currentStep?: string;
  onSelect: (key: string) => void;
}): JSX.Element {
  const rest = [block.more > 0 ? `${block.more} more` : "", block.tail].filter((part) => part !== "");
  return (
    <>
      {block.steps.map((step) => {
        const state = stepState(step);
        const current = currentStep === step.key;
        const classes = ["row", "step"];
        if (state === "held") classes.push("held");
        if (current) classes.push("cur");
        return (
          <div
            class={classes.join(" ")}
            role="link"
            data-key={step.key}
            aria-current={current ? "true" : undefined}
            onClick={() => onSelect(step.key)}
          >
            <StateGlyph state={state} />
            <span class="label">{step.label}</span>
            <Pips cells={step.pips.map((kind) => pipTone[kind])} />
            <span class="doing">{step.doing}</span>
          </div>
        );
      })}
      {rest.length > 0 && <div class="fold-line">{rest.join(" · ")}</div>}
    </>
  );
}

import type { JSX } from "preact";

import type { DesignLedgerItem, DesignStage, DesignStep } from "./gen/orrery/v1/designs_pb";
import { whoOfMaker } from "./stagewho";

// verdictWords is each steelman verdict in plain words, as round 8 words it. A verdict it does not name is drawn as it is.
const verdictWords: Readonly<Record<string, string>> = {
  READY: "ready",
  REVISE: "revise",
  NEEDS_DECISION: "needs your decision",
  BLOCKED: "blocked",
  STILL_OPEN: "still open",
  CHANGE_INDUCED_CONCERN: "a change raised a concern",
};

// dispositionWords is each ledger item's disposition in plain words. A disposition it does not name is drawn as it is.
const dispositionWords: Readonly<Record<string, string>> = {
  open: "open",
  adopted: "adopted",
  rejected: "rejected",
  deferred: "deferred",
};

// runningStatus is the status of a step that has not returned.
const runningStatus = "running";

// verdictWord says a verdict in plain words.
function verdictWord(verdict: string): string {
  return verdictWords[verdict] ?? verdict;
}

// PassKind is one of the steelman's two passes: discovery, or closure.
type PassKind = "discovery" | "closure";

// budgetCell says one pass's cell: running while a step of its kind runs, used with the verdict of the step that counted,
// not used · skipped once closure was skipped, and 1 left otherwise.
function budgetCell(stage: DesignStage, pass: PassKind): { look: string; words: string } {
  if (stage.steps.some((step) => step.kind === pass && step.status === runningStatus)) {
    return { look: "now live", words: "running" };
  }
  const counted = pass === "discovery" ? stage.discoveryCounted : stage.closureCounted;
  if (counted) {
    const step = stage.steps.filter((candidate) => candidate.kind === pass && candidate.counted).at(-1);
    const words = step !== undefined && step.verdict !== "" ? `used · ${verdictWord(step.verdict)}` : "used";
    return { look: "done spent", words };
  }
  if (pass === "closure" && stage.closureSkipped) {
    return { look: "done spent", words: "not used · skipped" };
  }
  return { look: "ahead", words: "1 left" };
}

// Pass draws one discovery or closure step: its kind, its status, its verdict in plain words, and did not count when it does
// not count toward the budget.
function Pass({ step }: { step: DesignStep }): JSX.Element {
  return (
    <li class="ss-pass" data-step={step.step}>
      <span class="ss-kind">{step.kind}</span>
      <span class="ss-status">{step.status}</span>
      {step.verdict !== "" && (
        <span class="chip" title={`the steelman returned ${step.verdict}`}>
          {verdictWord(step.verdict)}
        </span>
      )}
      {!step.counted && <span class="ink-3">did not count</span>}
    </li>
  );
}

// reasonLine says why a rejected item was rejected, or the boundary a deferred one waits on, and nothing for the others.
function reasonLine(item: DesignLedgerItem): string {
  if (item.disposition === "rejected" && item.reason !== "") {
    return `Rejected because: ${item.reason}`;
  }
  if (item.disposition === "deferred" && item.boundary !== "") {
    return `Deferred until ${item.boundary}`;
  }
  return "";
}

// LedgerItem draws one finding: its id in code, its impact and disposition, who decided it, then its origin, change and
// evidence, and closure's word and note once closure has given them. An open item names no one. An item the conversation
// decided is marked as decided for you, and names its reason with its maker.
function LedgerItem({ item }: { item: DesignLedgerItem }): JSX.Element {
  const who = item.disposition === "open" ? "" : whoOfMaker(item.maker);
  const forYou = item.maker?.kind === "conversation";
  const reason = reasonLine(item);
  return (
    <li class="ss-item" data-item={item.id}>
      <div class="ss-head">
        <code>{item.id}</code>
        <span class="ss-impact">{item.impact}</span>
        <span class="chip">{dispositionWords[item.disposition] ?? item.disposition}</span>
      </div>
      {who !== "" && (
        <p class="ss-who">
          {forYou && <span class="chip">decided for you</span>}
          <span>{who}</span>
        </p>
      )}
      {reason !== "" && <p class="ss-why">{reason}</p>}
      <dl class="ss-fields">
        <dt>Origin</dt>
        <dd>{item.origin}</dd>
        <dt>Change</dt>
        <dd>{item.change}</dd>
        <dt>Evidence</dt>
        <dd>
          <code>{item.evidence}</code>
        </dd>
        {item.closureWord !== "" && (
          <>
            <dt>Closure</dt>
            <dd>
              {item.closureWord}
              {item.closureNote !== "" && ` · ${item.closureNote}`}
            </dd>
          </>
        )}
      </dl>
    </li>
  );
}

// DesignSteelmanProps is the design's stage: its steps, its ledger and the state of its budget.
export interface DesignSteelmanProps {
  stage: DesignStage;
}

// DesignSteelman draws a design's steelman: the budget's two cells in words, each discovery and closure step oldest first
// with its kind, status and verdict, and the ledger of its findings with who decided each. The budget has no control: the
// Director runs both passes. A stage with no discovery or closure step and no ledger item draws nothing.
export function DesignSteelman({ stage }: DesignSteelmanProps): JSX.Element | null {
  const passes = stage.steps.filter((step) => step.kind === "discovery" || step.kind === "closure");
  if (passes.length === 0 && stage.ledger.length === 0) {
    return null;
  }
  const cells: { pass: PassKind; name: string }[] = [
    { pass: "discovery", name: "Discovery · 1" },
    { pass: "closure", name: "Closure · at most 1" },
  ];
  return (
    <section class="design-steelman" aria-label="the steelman">
      <div class="track two budget" role="group" aria-label="the steelman's budget: one discovery, at most one closure">
        {cells.map(({ pass, name }) => {
          const cell = budgetCell(stage, pass);
          return (
            <div key={pass} class={`stage ${cell.look}`} data-cell={pass}>
              <span class="nm">{name}</span>
              <span class="ds">{cell.words}</span>
            </div>
          );
        })}
      </div>
      {passes.length > 0 && (
        <div class="ss-passes">
          <div class="sec sub">
            <span class="name">Passes</span>
          </div>
          <ol aria-label="the passes">
            {passes.map((step) => (
              <Pass key={step.step} step={step} />
            ))}
          </ol>
        </div>
      )}
      {stage.ledger.length > 0 && (
        <section class="ss-ledger" aria-label="the ledger">
          <div class="sec sub">
            <span class="name">{stage.frozen ? "Ledger, frozen" : "Ledger"}</span>
            <span class="n">
              {stage.ledger.length} {stage.ledger.length === 1 ? "finding" : "findings"}
            </span>
            {stage.frozen && <span class="rest">froze when closure started</span>}
          </div>
          <ol>
            {stage.ledger.map((item) => (
              <LedgerItem key={item.id} item={item} />
            ))}
          </ol>
        </section>
      )}
    </section>
  );
}

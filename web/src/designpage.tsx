import type { JSX } from "preact";

import { DesignCard } from "./designcard";
import { DesignSteelman } from "./designsteelman";
import type {
  Design,
  DesignApproach,
  DesignChoice,
  DesignContract,
  DesignDelegation,
  DesignLogLine,
  DesignQuestion,
  DesignStage,
  DesignStep,
  DesignsGetResponse,
} from "./gen/orrery/v1/designs_pb";
import { noticeClock } from "./noticewords";
import { type QuestionAnswer, QuestionThread } from "./questionthread";
import { whoOfLog, whoOfMaker } from "./stagewho";

// stageNames are the design's six stages in the order a design reaches them, as round 8 names them.
const stageNames = ["Research", "Questions", "Design", "Steelman", "Closure", "Proposed"] as const;

// Reached is the stage a design is at: one of the six, or done once every contract is accepted.
type Reached = (typeof stageNames)[number] | "done";

// reachedOf is the stage a design is at, by the first of these rules that holds: done when every contract is accepted;
// Proposed when any contract is proposed; Closure when closure was skipped or a closure step exists; Steelman when a
// discovery step exists; Design when an approach or a contract exists; Questions when a question exists; else Research.
// A design with no contracts is never done, since every contract being accepted needs a contract.
function reachedOf(stage: DesignStage, approaches: DesignApproach[], contracts: DesignContract[]): Reached {
  if (contracts.length > 0 && contracts.every((contract) => contract.state === "accepted")) {
    return "done";
  }
  if (contracts.some((contract) => contract.state === "proposed")) {
    return "Proposed";
  }
  if (stage.closureSkipped || stage.steps.some((step) => step.kind === "closure")) {
    return "Closure";
  }
  if (stage.steps.some((step) => step.kind === "discovery")) {
    return "Steelman";
  }
  if (approaches.length > 0 || contracts.length > 0) {
    return "Design";
  }
  if (stage.questions.length > 0) {
    return "Questions";
  }
  return "Research";
}

// StageStripProps names the stage the design is at.
interface StageStripProps {
  reached: Reached;
}

// StageStrip draws the design's six stages in a nav labelled where the design is. A stage before the reached one is done,
// and carries a check; the reached one is the current step; each stage after it says not reached. Once the design is done
// every stage is done and none is the current step. The strip opens nothing, so its stages are not links.
function StageStrip({ reached }: StageStripProps): JSX.Element {
  const current = reached === "done" ? stageNames.length : stageNames.indexOf(reached);
  return (
    <nav class="design-track" aria-label="where this design is">
      {stageNames.map((name, index) => {
        const state = index < current ? "done" : index === current ? "now" : "ahead";
        return (
          <div
            key={name}
            class={`stage ${state}`}
            data-stage={name}
            aria-current={state === "now" ? "step" : undefined}
          >
            <span class="nm">
              {state === "done" && <span aria-hidden="true">✓</span>}
              {name}
            </span>
            <span class="ds">{state === "ahead" ? "not reached" : ""}</span>
          </div>
        );
      })}
    </nav>
  );
}

// Decided draws what the Director decided for the owner: each delegation's message, quoted, then each decision under it,
// marked the Director, with its kind, its text and its reason. It draws nothing when no delegation is held.
function Decided({ delegations }: { delegations: DesignDelegation[] }): JSX.Element | null {
  if (delegations.length === 0) {
    return null;
  }
  return (
    <section class="design-decided" aria-label="Decided for you">
      <div class="sec">
        <span class="name">Decided for you</span>
      </div>
      {delegations.map((delegation) => (
        <div key={String(delegation.seq)} class="design-delegation">
          {delegation.message !== "" && <p class="design-delegation-message">{`“${delegation.message}”`}</p>}
          {delegation.decisions.length > 0 && (
            <ol>
              {delegation.decisions.map((decision, index) => (
                <li key={index} class="design-decision">
                  <span class="chip">the Director</span>
                  <span class="design-decision-kind">{decision.kind}</span>
                  <span class="design-decision-text">{decision.text}</span>
                  {decision.reason !== "" && <span class="design-decision-reason">{decision.reason}</span>}
                </li>
              ))}
            </ol>
          )}
        </div>
      ))}
    </section>
  );
}

// Contracts draws the design's contracts through DesignCard, under a head that counts them. It draws nothing for a design
// with no contracts.
function Contracts({ design, onOpenContract }: { design: Design; onOpenContract: (id: string) => void }): JSX.Element | null {
  if (design.contracts.length === 0) {
    return null;
  }
  return (
    <section class="design-contracts" aria-label="Contracts">
      <div class="sec">
        <span class="name">Contracts</span>
        <span class="n">{design.contracts.length}</span>
      </div>
      <DesignCard design={design} onOpenContract={onOpenContract} />
    </section>
  );
}

// Fact draws one of an approach's facts as a term and its description, and nothing when the fact is empty.
function Fact({ label, value }: { label: string; value: string }): JSX.Element | null {
  if (value === "") {
    return null;
  }
  return (
    <>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </>
  );
}

// ApproachCardProps are one approach the Director compared, and whether it is the one chosen.
interface ApproachCardProps {
  approach: DesignApproach;
  chosen: boolean;
}

// ApproachCard draws one approach: its name, marked recommended when it is and chosen when it is, then its facts.
function ApproachCard({ approach, chosen }: ApproachCardProps): JSX.Element {
  const classes = ["design-approach", approach.recommended && "recommended", chosen && "chosen"].filter(Boolean).join(" ");
  return (
    <li class={classes} data-approach={String(approach.seq)}>
      <div class="design-approach-head">
        <span class="nm">{approach.name}</span>
        {approach.recommended && <span class="chip">recommended</span>}
        {chosen && <span class="chip ok">chosen</span>}
      </div>
      <dl class="design-approach-facts">
        <Fact label="Shape" value={approach.shape} />
        <Fact label="Costs" value={approach.cost} />
        <Fact label="Why this one" value={approach.wins} />
        <Fact label="Where it loses" value={approach.loses} />
        <Fact label="Reconsider when" value={approach.reconsider} />
        {approach.research.length > 0 && (
          <>
            <dt>From research</dt>
            <dd>
              {approach.research.map((line, index) => (
                <code key={index}>{line}</code>
              ))}
            </dd>
          </>
        )}
      </dl>
    </li>
  );
}

// ApproachesProps are the approaches the Director compared, and the choice made between them, when one was.
interface ApproachesProps {
  approaches: DesignApproach[];
  choice: DesignChoice | undefined;
}

// Approaches draws the approaches the Director compared, each with the recommended one marked, then the choice with who
// made it, in whoOfMaker's words. It draws nothing when no approach is held.
function Approaches({ approaches, choice }: ApproachesProps): JSX.Element | null {
  if (approaches.length === 0) {
    return null;
  }
  return (
    <section class="design-approaches" aria-label="Approach">
      <div class="sec">
        <span class="name">Approach</span>
        <span class="n">{`${approaches.length} compared`}</span>
      </div>
      <ol class="design-approach-list">
        {approaches.map((approach) => (
          <ApproachCard
            key={String(approach.seq)}
            approach={approach}
            chosen={choice !== undefined && choice.approach === approach.seq}
          />
        ))}
      </ol>
      {choice !== undefined && (
        <p class="design-choice">
          <span class="chip ok">chosen</span>
          <span>{whoOfMaker(choice.maker)}</span>
          {choice.words !== "" && <span class="design-choice-words">{choice.words}</span>}
        </p>
      )}
    </section>
  );
}

// linesOf returns the log lines that name question id, in the log's order.
function linesOf(log: DesignLogLine[], id: bigint): DesignLogLine[] {
  return log.filter((line) => line.ids.includes(`Q${id}`));
}

// QuestionsProps are the design's questions, and the log whose lines each thread draws, with the answer a question takes.
interface QuestionsProps {
  questions: DesignQuestion[];
  log: DesignLogLine[];
  onAnswer: (question: bigint, answer: QuestionAnswer) => Promise<void>;
}

// Questions draws each of the design's questions as its own thread in the design, with the log lines that name it, under a
// head that counts the settled ones. It draws nothing for a design with no question.
function Questions({ questions, log, onAnswer }: QuestionsProps): JSX.Element | null {
  if (questions.length === 0) {
    return null;
  }
  const settled = questions.filter((question) => question.answered !== 0n).length;
  return (
    <section class="design-questions" aria-label="Questions">
      <div class="sec">
        <span class="name">Questions</span>
        <span class="n">{`${settled} of ${questions.length} settled`}</span>
      </div>
      {questions.map((question) => (
        <QuestionThread
          key={String(question.id)}
          question={question}
          lines={linesOf(log, question.id)}
          place="design"
          onAnswer={(answer) => onAnswer(question.id, answer)}
        />
      ))}
    </section>
  );
}

// ScoutReport is a research step's report as the scout returned it, with the parts the page draws.
interface ScoutReport {
  answer: string;
  evidence: string[];
  notInspected: string;
}

// scoutStatuses are the statuses a scout's return may carry, as internal/stage/scout.json names them.
const scoutStatuses: readonly string[] = ["done", "blocked"];

// isStringArray reports whether value is an array of strings.
function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === "string");
}

// scoutReport reads a report as the scout's return: an object whose status is done or blocked, whose answer and
// not_inspected are text, and whose evidence is a list of text. It returns null for any text that is not that return.
function scoutReport(text: string): ScoutReport | null {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return null;
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return null;
  }
  const { status, answer, evidence, not_inspected: notInspected } = value as Record<string, unknown>;
  if (typeof status !== "string" || !scoutStatuses.includes(status)) {
    return null;
  }
  if (typeof answer !== "string" || typeof notInspected !== "string" || !isStringArray(evidence)) {
    return null;
  }
  return { answer, evidence, notInspected };
}

// ReportBody draws a research step's report: the scout's answer, each evidence line in code, and what was not inspected.
// A report that is not the scout's return is drawn as its text. A step with no report draws nothing.
function ReportBody({ text }: { text: string }): JSX.Element | null {
  if (text === "") {
    return null;
  }
  const report = scoutReport(text);
  if (report === null) {
    return <pre class="design-report-text">{text}</pre>;
  }
  return (
    <dl class="design-report">
      <Fact label="Answer" value={report.answer} />
      {report.evidence.length > 0 && (
        <>
          <dt>Evidence</dt>
          <dd>
            <ul class="design-evidence">
              {report.evidence.map((line, index) => (
                <li key={index}>
                  <code>{line}</code>
                </li>
              ))}
            </ul>
          </dd>
        </>
      )}
      <Fact label="Not inspected" value={report.notInspected} />
    </dl>
  );
}

// Research draws each research step the design started: its subject, its status, and its report. It draws nothing for a
// design with no research step.
function Research({ steps }: { steps: DesignStep[] }): JSX.Element | null {
  const research = steps.filter((step) => step.kind === "research");
  if (research.length === 0) {
    return null;
  }
  return (
    <section class="design-research" aria-label="Research">
      <div class="sec">
        <span class="name">Research</span>
      </div>
      <ol>
        {research.map((step) => (
          <li key={step.step} class="design-research-step" data-step={step.step}>
            <div class="design-research-head">
              <span class="design-research-subject">{step.subject}</span>
              <span class="chip">{step.status}</span>
            </div>
            <ReportBody text={step.report} />
          </li>
        ))}
      </ol>
    </section>
  );
}

// Log draws the design's log, oldest first: each line's time in code, who did it in whoOfLog's words, what was done, and
// under it its words. It draws nothing for a design with no line.
function Log({ lines }: { lines: DesignLogLine[] }): JSX.Element | null {
  if (lines.length === 0) {
    return null;
  }
  return (
    <section class="design-log" aria-label="Log">
      <div class="sec">
        <span class="name">Log</span>
      </div>
      <ol>
        {lines.map((line) => (
          <li key={String(line.seq)} class="design-log-line">
            <div class="design-log-head">
              <code>{noticeClock(line.time)}</code>
              <span class="design-log-who">{whoOfLog(line.maker?.kind ?? "")}</span>
              <span class="design-log-what">{line.what}</span>
            </div>
            {line.words !== "" && <p class="design-log-words">{line.words}</p>}
          </li>
        ))}
      </ol>
    </section>
  );
}

// DesignPageProps are the design as the list holds it, the response that holds its record, the error of the last request
// when there is one, the press that opens a contract by its id, and the answer a question takes.
export interface DesignPageProps {
  design: Design;
  get: DesignsGetResponse | null;
  error: string;
  onOpenContract: (id: string) => void;
  onAnswer: (question: bigint, answer: QuestionAnswer) => Promise<void>;
}

// DesignPage draws a design's page in round 8's order: its stage strip, what the Director decided for the owner, its
// contracts, its steelman, its approach, its questions, its research and its log. The stage strip, the steelman, the
// questions and the research come from the record's stage. While the record is loading the page draws a loading line in
// the strip's place, and with an error it draws the error line there; a record with no stage draws the line that says so
// there. A part the record does not hold is not drawn.
export function DesignPage({ design, get, error, onOpenContract, onAnswer }: DesignPageProps): JSX.Element {
  const staged =
    error === "" && get !== null && get.stage !== undefined
      ? { stage: get.stage, approaches: get.approaches, log: get.log }
      : null;
  let stageLine: JSX.Element;
  if (error !== "") {
    stageLine = <p class="inote error" role="alert">{error}</p>;
  } else if (get === null) {
    stageLine = <p class="inote">the design is loading</p>;
  } else if (staged === null) {
    stageLine = <p class="inote">the stage record is unavailable</p>;
  } else {
    stageLine = <StageStrip reached={reachedOf(staged.stage, staged.approaches, design.contracts)} />;
  }
  return (
    <div class="design-page">
      {stageLine}
      {get !== null && <Decided delegations={get.delegations} />}
      <Contracts design={design} onOpenContract={onOpenContract} />
      {staged !== null && <DesignSteelman stage={staged.stage} />}
      {get !== null && <Approaches approaches={get.approaches} choice={get.choice} />}
      {staged !== null && <Questions questions={staged.stage.questions} log={staged.log} onAnswer={onAnswer} />}
      {staged !== null && <Research steps={staged.stage.steps} />}
      {get !== null && <Log lines={get.log} />}
    </div>
  );
}

import { Fragment, type ComponentChildren, type JSX, type Ref } from "preact";

import { CommitMark } from "./commitmark";
import { lengthOf, type RowAbout, ratingTone } from "./contracts";
import type { ContractDecision } from "./gen/orrery/v1/contracts_pb";
import type { DesignLogLine, DesignQuestion } from "./gen/orrery/v1/designs_pb";
import type { EpicContract, EpicRequirement } from "./gen/orrery/v1/epics_pb";
import { type QuestionAnswer, QuestionThread } from "./questionthread";
import { clock } from "./reader";

// DocMarks are the marks a version of a contract wears on its document: the rows that open decisions concern, whether
// the irreversible section is still unread, the rows and sections the version changed, and the version.
export interface DocMarks {
  concerned: ReadonlySet<string>;
  unread: boolean;
  changedRows: ReadonlySet<string>;
  changedSections: ReadonlySet<string>;
  version: number;
}

// ContractDesign is the design a contract belongs to, as its record holds it: the questions of the design that concern
// the contract, the log its questions' threads hold, and the answer a question takes, the design page's own.
export interface ContractDesign {
  questions: DesignQuestion[];
  log: DesignLogLine[];
  onAnswer: (question: bigint, answer: QuestionAnswer) => Promise<void>;
}

interface ContractBodyProps {
  contract: EpicContract;
  requirements: EpicRequirement[];
  // decisions is the contract's decisions. Absent, the document is an Epics.Get contract's, and its Decisions section
  // reads None.
  decisions?: ContractDecision[];
  // design is the design the contract belongs to, whose questions about the contract are drawn as threads after the
  // decisions. Absent, the contract carries no question.
  design?: ContractDesign;
  // record is the decision line's record words. Absent, it is the accepted clock of the contract.
  record?: string;
  // facts is drawn last in the decision line.
  facts?: ComponentChildren;
  marks?: DocMarks;
  // attending is whether the version is the newest of a proposed contract, the one the viewer is deciding. Absent, it is.
  // Only while it is attending do its open decisions count, draw attention, and mark their rows, sections and the marks.
  attending?: boolean;
  // aboutId is the id of the row the dock's About names. That row's control is drawn lit.
  aboutId?: string;
  // onAbout, when given, gives each row a control that talks about it, and each addition Keep and Strike.
  onAbout?: (about: RowAbout) => void;
  irreversibleRef?: Ref<HTMLElement>;
}

const noMarks: DocMarks = {
  concerned: new Set(),
  unread: false,
  changedRows: new Set(),
  changedSections: new Set(),
  version: 0,
};

// ContractBody draws a contract as the contract document: its decision line, then its ten sections in order, each under its
// name with its column names, how-bad chips, id chips by kind, attention marks and changed chips. Every text is a child
// of its element, so no markup in a field becomes an element.
export function ContractBody(props: ContractBodyProps): JSX.Element {
  const { contract, requirements, decisions = [], onAbout, aboutId, irreversibleRef, design } = props;
  const attending = props.attending ?? true;
  const marks = props.marks ?? noMarks;
  const record = props.record ?? `accepted ${clock(contract.accepted)}`;
  const pending = decisions.filter((decision) => decision.answer === "");
  const settled = decisions.filter((decision) => decision.answer !== "");
  // counted are the open decisions the record counts, and they count only while the version is attending.
  const counted = attending ? pending : [];
  const unread = attending && marks.unread;
  // carried are the questions the design gives this contract, and openCarried are those with no answer yet.
  const carried = design === undefined ? [] : design.questions;
  const openCarried = carried.filter((question) => question.answered === 0n);
  const length = lengthOf(contract, requirements, decisions);

  const changedRow = (id: string): number | undefined => (marks.changedRows.has(id) ? marks.version : undefined);
  const changedIn = (...fields: string[]): number | undefined =>
    fields.some((field) => marks.changedSections.has(field)) ? marks.version : undefined;
  const attends = (id: string): boolean => attending && marks.concerned.has(id);

  const setter = contract.failures.find((failure) => failure.rating === contract.care);
  const careLine =
    setter === undefined ? `Level of care: ${contract.care}` : `Level of care: ${contract.care}, set by ${setter.id}`;

  return (
    <article class="contract">
      <section class="dline">
        <h2>{contract.title}</h2>
        {contract.approved !== "" && <p class="approved">{contract.approved}</p>}
        <div class="facts">
          <span>
            Level of care <RatingChip rating={contract.care} />
          </span>
          <span>
            Decisions needed: <b class={counted.length > 0 ? "is-attention" : "ink-1"}>{counted.length > 0 ? counted.length : "none"}</b>
          </span>
          <span class="ink-3">{record}</span>
          <p class="length" title="Its length is a signal, never a refusal: Accept is not blocked by it.">
            {lengthWords(length)}
          </p>
          {contract.base !== "" && <CommitMark hash={contract.base} />}
          {props.facts}
        </div>
      </section>

      <Section name="What you asked for" changed={changedIn("intent")}>
        <p class="intent">{contract.intent}</p>
      </Section>

      <Section
        name="What could go wrong, and how much we care"
        attention={contract.failures.some((failure) => attends(failure.id))}
        changed={changedIn("failures", "care")}
      >
        <Table head={["", "if this happened", "how bad", "what we do"]}>
          {contract.failures.map((failure) => (
            <Row key={failure.id} id={failure.id} attention={attends(failure.id)}>
              <span>
                <IdChip id={failure.id} />
              </span>
              <span class="strong wide">
                {failure.text}
                <ChangedChip version={changedRow(failure.id)} />
              </span>
              <span>
                <RatingChip rating={failure.rating} />
              </span>
              <span class="wide">
                {failure.response}
                {onAbout !== undefined && (
                  <Talk
                    about={{ id: failure.id, text: failure.text }}
                    lit={aboutId === failure.id}
                    onAbout={onAbout}
                  />
                )}
              </span>
            </Row>
          ))}
        </Table>
        <p class="inote">
          <span>{careLine}</span>
        </p>
      </Section>

      <Section
        name="Things you did not ask for"
        n={contract.additions.length > 0 ? contract.additions.length : undefined}
        changed={changedIn("additions")}
      >
        {contract.additions.length === 0 ? (
          <p class="inote">
            <span>None.</span>
          </p>
        ) : (
          <Table head={["where", "what", "why", "cost", ""]}>
            {contract.additions.map((addition, index) => (
              <Row key={index}>
                <span class="wide">{addition.location}</span>
                <span class="strong wide">{addition.what}</span>
                <span class="wide">{addition.why}</span>
                <span class="wide ink-3">{addition.cost}</span>
                <span class="end">
                  {onAbout !== undefined && (
                    <span class="keepdrop">
                      <button
                        type="button"
                        class="btn"
                        onClick={() => onAbout({ id: addition.location, text: `Keep: ${addition.what}` })}
                      >
                        Keep
                      </button>
                      <button
                        type="button"
                        class="btn quiet"
                        onClick={() => onAbout({ id: addition.location, text: `Strike: ${addition.what}` })}
                      >
                        Strike
                      </button>
                    </span>
                  )}
                </span>
              </Row>
            ))}
          </Table>
        )}
      </Section>

      <Section
        name="What will be true when done"
        n={requirements.length > 0 ? requirements.length : undefined}
        attention={requirements.some((requirement) => attends(requirement.id))}
        changed={changedIn("requirements")}
      >
        <Table head={["", "must be true", "how we’ll know"]}>
          {requirements.map((requirement) => (
            <Row key={requirement.id} id={requirement.id} attention={attends(requirement.id)}>
              <span>
                <IdChip id={requirement.id} />
              </span>
              <span class="strong wide">
                {requirement.text}
                <ChangedChip version={changedRow(requirement.id)} />
              </span>
              <span class="wide">
                {requirement.evidence}
                {onAbout !== undefined && (
                  <Talk
                    about={{ id: requirement.id, text: requirement.text }}
                    lit={aboutId === requirement.id}
                    onAbout={onAbout}
                  />
                )}
              </span>
            </Row>
          ))}
        </Table>
      </Section>

      <Section
        name="What I’m assuming"
        n={contract.premises.length > 0 ? contract.premises.length : undefined}
        attention={contract.premises.some((premise) => attends(premise.id))}
        changed={changedIn("premises")}
      >
        <Table head={["", "assumption", "if wrong"]}>
          {contract.premises.map((premise) => (
            <Row key={premise.id} id={premise.id} attention={attends(premise.id)}>
              <span>
                <IdChip id={premise.id} />
              </span>
              <span class="strong wide">
                {premise.text}
                <ChangedChip version={changedRow(premise.id)} />
              </span>
              <span class="wide">
                {premise.ifWrong}
                {onAbout !== undefined && (
                  <Talk about={{ id: premise.id, text: premise.text }} lit={aboutId === premise.id} onAbout={onAbout} />
                )}
              </span>
            </Row>
          ))}
        </Table>
      </Section>

      <Section name="What we won’t do" changed={changedIn("exclusions")}>
        <ul class="exclusions">
          {contract.exclusions.map((exclusion) => (
            <li class="row">{exclusion}</li>
          ))}
        </ul>
      </Section>

      <Section name="How, and why not the other ways" changed={changedIn("approach", "alternatives")}>
        <p class="approach">{contract.approach}</p>
        <Table head={["alternative", "why not", "reconsider when"]}>
          {contract.alternatives.map((alternative) => (
            <Row>
              <span class="strong wide">{alternative.alternative}</span>
              <span class="wide">{alternative.whyNot}</span>
              <span class="wide ink-3">{alternative.reconsider}</span>
            </Row>
          ))}
        </Table>
      </Section>

      <Section
        name="What leaves this machine or can’t be undone"
        attention={unread}
        rest={unread ? <span class="unread">read this before Accept</span> : undefined}
        changed={changedIn("release_steps", "release_then")}
        sectionRef={irreversibleRef}
      >
        <Table head={["", "step", "target", "undo"]}>
          {contract.releaseSteps.map((step, index) => (
            <Row>
              <span class="id">{index + 1}</span>
              <span class="strong wide">{step.step}</span>
              <span class="wide">{step.target}</span>
              <span>{step.undo}</span>
            </Row>
          ))}
        </Table>
        <p class="inote">
          <span>{contract.releaseThen}</span>
        </p>
      </Section>

      <Section
        name="Decisions I need from you"
        attention={counted.length > 0}
        n={counted.length > 0 ? counted.length : undefined}
        changed={changedIn("decisions")}
      >
        {decisions.length === 0 && carried.length === 0 && (
          <p class="inote">
            <span>None.</span>
          </p>
        )}
        {pending.length > 0 && (
          <Table head={["", "question", "recommended", "why"]}>
            {pending.map((decision) => (
              <Fragment key={decision.id}>
                <Row id={decision.id} attention={attending}>
                  <span>
                    <IdChip id={decision.id} />
                  </span>
                  <span class="strong wide">
                    {decision.question}
                    <ChangedChip version={changedRow(decision.id)} />
                  </span>
                  <span class="wide">
                    {decision.recommendation !== "" && !decision.options.includes(decision.recommendation) && (
                      <>
                        <span class="chip">recommended</span> {decision.recommendation}
                      </>
                    )}
                  </span>
                  <span class="wide ink-3">
                    {decision.reason}
                    {onAbout !== undefined && (
                      <Talk
                        about={{ id: decision.id, text: decision.question }}
                        lit={aboutId === decision.id}
                        onAbout={onAbout}
                      />
                    )}
                  </span>
                </Row>
                {decision.options.map((option, index) => (
                  <div key={index} class="tr option">
                    <span />
                    <span class="wide">{option}</span>
                    <span>{option === decision.recommendation && <span class="chip">recommended</span>}</span>
                    <span />
                  </div>
                ))}
              </Fragment>
            ))}
          </Table>
        )}
        {settled.map((decision) => (
          <p class="inote ok">
            <span>
              {pending.length === 0 && openCarried.length === 0 ? "None. " : ""}Settled: {decision.id}{" "}
              {decision.answer}
            </span>
          </p>
        ))}
        {design !== undefined &&
          carried.map((question) => (
            <QuestionThread
              key={String(question.id)}
              question={question}
              lines={design.log.filter((line) => line.ids.includes(`Q${question.id}`))}
              place="design"
              onAnswer={(answer) => design.onAnswer(question.id, answer)}
            />
          ))}
      </Section>

      <Section name="Checks the machines run" changed={changedIn("checks")}>
        <p class="checks">{contract.checks}</p>
      </Section>
    </article>
  );
}

// longFrom and veryLongFrom are the line counts at which the length meter says long and very long.
const longFrom = 41;
const veryLongFrom = 61;

// lengthWords are the meter's words for a contract of the lines given: its count, then long from 41 lines and very long
// from 61.
function lengthWords(lines: number): string {
  const count = `${lines} lines`;
  if (lines >= veryLongFrom) {
    return `${count} · very long`;
  }
  return lines >= longFrom ? `${count} · long` : count;
}

// RatingChip draws a rating as a chip in its tone.
function RatingChip({ rating }: { rating: string }): JSX.Element {
  const tone = ratingTone(rating);
  return <span class={tone === "" ? "chip" : `chip ${tone}`}>{rating}</span>;
}

// IdChip draws a row's id, with its kind, the first letter of the id, as data-k.
function IdChip({ id }: { id: string }): JSX.Element {
  return (
    <span class="idc" data-k={id.charAt(0)}>
      {id}
    </span>
  );
}

// ChangedChip says the version changed what it is in, and draws nothing when the version did not change it.
function ChangedChip({ version }: { version: number | undefined }): JSX.Element | null {
  if (version === undefined) {
    return null;
  }
  return <span class="chip new">{`changed in v${version}`}</span>;
}

// Section draws one of the document's sections: its name, its count, its tag, its changed chip, and its body.
function Section(props: {
  name: string;
  n?: number;
  attention?: boolean;
  rest?: ComponentChildren;
  changed?: number;
  sectionRef?: Ref<HTMLElement>;
  children: ComponentChildren;
}): JSX.Element {
  const { name, n, attention = false, rest, changed, sectionRef, children } = props;
  return (
    <section aria-label={name} ref={sectionRef}>
      <div class={attention ? "sec attention" : "sec"}>
        <span class="name">{name}</span>
        {n !== undefined && <span class="n">{n}</span>}
        {rest !== undefined && <span class="rest">{rest}</span>}
        <ChangedChip version={changed} />
      </div>
      {children}
    </section>
  );
}

// Table draws a table: its column names in a head row, then its rows.
function Table(props: { head: string[]; children: ComponentChildren }): JSX.Element {
  return (
    <div class="tbl">
      <div class="tr head">
        {props.head.map((name) => (
          <span>{name}</span>
        ))}
      </div>
      {props.children}
    </div>
  );
}

// Row draws a table row. A row with an id that an open decision concerns, or an open decision itself, is attention.
function Row(props: { id?: string; attention?: boolean; children: ComponentChildren }): JSX.Element {
  return (
    <div class={props.attention === true ? "tr attention" : "tr"} data-row={props.id}>
      {props.children}
    </div>
  );
}

// Talk is a row's control: the chat icon alone, named Talk about this row, which talks about that row. It is lit while
// the dock's About names its row.
function Talk(props: { about: RowAbout; lit: boolean; onAbout: (about: RowAbout) => void }): JSX.Element {
  const { about, lit, onAbout } = props;
  return (
    <span class={lit ? "say on" : "say"}>
      <button
        type="button"
        class="ctl only"
        aria-label="Talk about this row"
        title="Talk about this row"
        onClick={() => onAbout(about)}
      >
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true" fill="none" stroke="currentColor">
          <path d="M3 3h10v7H8l-3 3v-3H3z" />
        </svg>
      </button>
    </span>
  );
}

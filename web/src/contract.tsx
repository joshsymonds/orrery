import type { ComponentChildren, JSX, Ref } from "preact";

import { CommitMark } from "./commitmark";
import type { ContractDecision } from "./gen/orrery/v1/contracts_pb";
import type { EpicContract, EpicRequirement } from "./gen/orrery/v1/epics_pb";
import { clock } from "./reader";

// RowAbout is the row a control talks about: its id, and the text the row shows.
export interface RowAbout {
  id: string;
  text: string;
}

// DocMarks are the marks the newest version of a contract wears on its document: the rows that open decisions concern,
// whether the irreversible section is still unread, the rows and sections the version changed, and the version.
export interface DocMarks {
  concerned: ReadonlySet<string>;
  unread: boolean;
  changedRows: ReadonlySet<string>;
  changedSections: ReadonlySet<string>;
  version: number;
}

interface ContractBodyProps {
  contract: EpicContract;
  requirements: EpicRequirement[];
  // decisions is the contract's decisions. Absent, the document is an Epics.Get contract's, and its Decisions section
  // reads None.
  decisions?: ContractDecision[];
  // record is the decision line's record words. Absent, it is the accepted clock of the contract.
  record?: string;
  // facts is drawn last in the decision line.
  facts?: ComponentChildren;
  marks?: DocMarks;
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

// ContractBody draws a contract as Gambit's document: its decision line, then its ten sections in order, each under its
// name with its column names, how-bad chips, id chips by kind, attention marks and changed chips. Every text is a child
// of its element, so no markup in a field becomes an element.
export function ContractBody(props: ContractBodyProps): JSX.Element {
  const { contract, requirements, decisions = [], onAbout, irreversibleRef } = props;
  const marks = props.marks ?? noMarks;
  const record = props.record ?? `accepted ${clock(contract.accepted)}`;
  const open = decisions.filter((decision) => decision.answer === "");
  const settled = decisions.filter((decision) => decision.answer !== "");

  const changedRow = (id: string): number | undefined => (marks.changedRows.has(id) ? marks.version : undefined);
  const changedIn = (...fields: string[]): number | undefined =>
    fields.some((field) => marks.changedSections.has(field)) ? marks.version : undefined;
  const attends = (id: string): boolean => marks.concerned.has(id);

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
            Decisions needed: <b class={open.length > 0 ? "is-attention" : "ink-1"}>{open.length > 0 ? open.length : "none"}</b>
          </span>
          <span class="ink-3">{record}</span>
          {contract.base !== "" && <CommitMark hash={contract.base} />}
          {props.facts}
        </div>
      </section>

      <Section name="What you asked for" changed={changedIn("intent")}>
        <p class="intent">{contract.intent}</p>
      </Section>

      <Section
        name="What could go wrong, and how much we care"
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
                  <Talk about={{ id: failure.id, text: failure.text }} onAbout={onAbout} />
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
                  <Talk about={{ id: requirement.id, text: requirement.text }} onAbout={onAbout} />
                )}
              </span>
            </Row>
          ))}
        </Table>
      </Section>

      <Section
        name="What I’m assuming"
        n={contract.premises.length > 0 ? contract.premises.length : undefined}
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
                {onAbout !== undefined && <Talk about={{ id: premise.id, text: premise.text }} onAbout={onAbout} />}
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
        attention={marks.unread}
        rest={marks.unread ? <span class="unread">read this before Accept</span> : undefined}
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
        attention={open.length > 0}
        n={open.length > 0 ? open.length : undefined}
        changed={changedIn("decisions")}
      >
        {decisions.length === 0 && (
          <p class="inote">
            <span>None.</span>
          </p>
        )}
        {open.length > 0 && (
          <Table head={["", "question", "recommended", "why"]}>
            {open.map((decision) => (
              <Row key={decision.id} id={decision.id} attention>
                <span>
                  <IdChip id={decision.id} />
                </span>
                <span class="wide">
                  <span class="strong">{decision.question}</span> <span class="ink-2">{decision.options.join(" · ")}</span>
                  <ChangedChip version={changedRow(decision.id)} />
                </span>
                <span class="wide">{decision.recommendation}</span>
                <span class="wide ink-3">
                  {decision.reason}
                  {onAbout !== undefined && (
                    <Talk about={{ id: decision.id, text: decision.question }} onAbout={onAbout} />
                  )}
                </span>
              </Row>
            ))}
          </Table>
        )}
        {settled.map((decision) => (
          <p class="inote ok">
            <span>
              {open.length === 0 ? "None. " : ""}Settled: {decision.id} {decision.answer}
            </span>
          </p>
        ))}
      </Section>

      <Section name="Checks the machines run" changed={changedIn("checks")}>
        <p class="checks">{contract.checks}</p>
      </Section>
    </article>
  );
}

// ratingTone is the tone of a rating: limited is quiet, serious live, and severe bad.
function ratingTone(rating: string): "" | "live" | "bad" {
  switch (rating) {
    case "serious":
      return "live";
    case "severe":
      return "bad";
    default:
      return "";
  }
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

// Talk is a row's control: the chat icon alone, named Talk about this row, which talks about that row.
function Talk(props: { about: RowAbout; onAbout: (about: RowAbout) => void }): JSX.Element {
  const { about, onAbout } = props;
  return (
    <span class="say">
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

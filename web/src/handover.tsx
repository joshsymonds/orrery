import type { JSX } from "preact";

import type {
  EpicClosure,
  EpicLedgerFinding,
  EpicReleaseRun,
  EpicReleaseStep,
  EpicReviewVerdict,
  EpicsGetResponse,
} from "./gen/orrery/v1/epics_pb";

// Handover is an orreryd epic's handover: its phase, then its REVIEW verdicts, LEDGER findings and RELEASE steps, each
// section drawn only when the response holds its list. It draws nothing when the response has no reviews, ledger findings
// or release runs. Every value is the response's, and every text is a child of its element, so no markup in a claim,
// evidence or reason becomes an element.
export function Handover(props: { detail: EpicsGetResponse }): JSX.Element | null {
  const { detail } = props;
  const { reviews, ledger, releases } = detail;
  if (reviews.length === 0 && ledger.length === 0 && releases.length === 0) {
    return null;
  }
  return (
    <div class="handover">
      <p>{`phase ${detail.epic?.outcome ?? ""}`}</p>
      {reviews.length > 0 && <ReviewSection verdicts={reviews} />}
      {ledger.length > 0 && <LedgerSection findings={ledger} />}
      {releases.length > 0 && <ReleaseSection steps={detail.contract?.releaseSteps ?? []} runs={releases} />}
    </div>
  );
}

// ReviewSection draws the epic-stage reviewers' verdicts in position order: each reviewer and verdict, then its reason and
// the step that ran its review when set.
function ReviewSection(props: { verdicts: EpicReviewVerdict[] }): JSX.Element {
  const { verdicts } = props;
  return (
    <section>
      <h3 class="eyebrow">
        REVIEW<span class="sub">{verdicts.length}</span>
      </h3>
      <ol>
        {verdicts.map((verdict, index) => (
          <li key={index}>
            <span class="path">{verdict.reviewer}</span>
            <span class="state">{verdict.verdict}</span>
            {verdict.reason !== "" && <p>{verdict.reason}</p>}
            {verdict.step !== "" && <p>{`step ${verdict.step}`}</p>}
          </li>
        ))}
      </ol>
    </section>
  );
}

// LedgerSection draws the end-of-epic review ledger in ledger order: each finding's id, reviewer and result, or unverified
// when it was left so, then its claim, location and reason when set, then its closures in round order.
function LedgerSection(props: { findings: EpicLedgerFinding[] }): JSX.Element {
  const { findings } = props;
  return (
    <section>
      <h3 class="eyebrow">LEDGER</h3>
      <ol>
        {findings.map((finding, index) => (
          <li key={index}>
            <span class="path">{finding.id}</span>
            <span class="kind">{finding.reviewer}</span>
            <span class="state">{finding.unverified ? "unverified" : finding.result}</span>
            <p>{finding.claim}</p>
            {finding.location !== "" && <p>{finding.location}</p>}
            {finding.reason !== "" && <p>{finding.reason}</p>}
            {finding.closures.length > 0 && (
              <ul>
                {finding.closures.map((closure, closureIndex) => (
                  <li key={closureIndex}>{closureText(closure)}</li>
                ))}
              </ul>
            )}
          </li>
        ))}
      </ol>
    </section>
  );
}

// closureText is a closure's line: its round and result, then its reason when set.
function closureText(closure: EpicClosure): string {
  const text = `round ${closure.round} · ${closure.result}`;
  return closure.reason === "" ? text : `${text} · ${closure.reason}`;
}

// ReleaseSection draws the contract's release steps in order, each with the release runs of its position nested under it,
// then every other run in response order. A release run whose position names no step draws after the steps too, so no run
// the response holds goes unshown.
function ReleaseSection(props: { steps: EpicReleaseStep[]; runs: EpicReleaseRun[] }): JSX.Element {
  const { steps, runs } = props;
  const inStep = (run: EpicReleaseRun): boolean =>
    run.part === "release" && run.position >= 1n && run.position <= BigInt(steps.length);
  const others = runs.filter((run) => !inStep(run));
  return (
    <section>
      <h3 class="eyebrow">
        RELEASE<span class="sub">{runs.length}</span>
      </h3>
      <ol>
        {steps.map((step, index) => {
          const stepRuns = runs.filter((run) => run.part === "release" && run.position === BigInt(index + 1));
          return (
            <li key={index}>
              <span class="path">{step.step}</span>
              <span class="kind">{step.target}</span>
              <span>{`undo ${step.undo}`}</span>
              {stepRuns.length > 0 && (
                <ul>
                  {stepRuns.map((run, runIndex) => (
                    <li key={runIndex}>
                      <RunRow run={run} />
                    </li>
                  ))}
                </ul>
              )}
            </li>
          );
        })}
        {others.map((run, index) => (
          <li key={index}>
            <span class="kind">{run.part}</span>
            <RunRow run={run} />
          </li>
        ))}
      </ol>
    </section>
  );
}

// RunRow is a release run's row: its attempt, its end when set, whether it passed, its head and commits when set, then its
// reason and evidence when set.
function RunRow(props: { run: EpicReleaseRun }): JSX.Element {
  const { run } = props;
  return (
    <>
      <span>{`attempt ${run.attempt}`}</span>
      {run.end !== "" && <span class="end">{run.end}</span>}
      <span class="state">{run.passed ? "passed" : "failed"}</span>
      {run.head !== "" && <span>{`head ${run.head}`}</span>}
      {run.commits.map((commit, index) => (
        <span key={index}>{`commit ${commit}`}</span>
      ))}
      {run.reason !== "" && <p>{run.reason}</p>}
      {run.evidence !== "" && <pre class="log">{run.evidence}</pre>}
    </>
  );
}

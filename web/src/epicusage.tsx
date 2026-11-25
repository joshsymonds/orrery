// EpicUsageSection draws an epic's Usage section as round 8 section E draws it: each session the epic's usage answers,
// largest list cost first, named by its step, with the model of its largest total, its summed requests, cache write tokens
// and list cost, and a bar of its list against the largest row's. The steerer, or a record epic's step caller, is shared:
// its row reads steering and is marked shared · not in total, and the header's list and billed sums leave it out. The
// header's sums are the step sessions' alone. Under narrow a row has no bar. The section draws nothing without a session.

import type { EpicRequirement, EpicSession, EpicStep } from "./gen/orrery/v1/epics_pb";
import type { UsageEpicSession } from "./gen/orrery/v1/usage_pb";
import { dollars, modelRows, sessionSum, tokens } from "./usagefigures";

// evidenceTask is the prefix of the task label an evidence judge's step carries, before the id of the run it judges.
const evidenceTask = "evidence-";

// compareText orders two strings by their code points, so the order does not depend on the locale.
function compareText(a: string, b: string): number {
  if (a < b) {
    return -1;
  }
  return a > b ? 1 : 0;
}

// stepNameOf names a step's session. An evidence judge reads its requirement and judge, the requirement being the one whose
// runs hold the run its task names; a judge no requirement holds reads its agent. A step with a task reads the task and
// implement, or review when it is read-only. Any other step reads its agent.
function stepNameOf(step: EpicStep, requirements: readonly EpicRequirement[]): string {
  if (step.role === "evidence") {
    const run = step.task.startsWith(evidenceTask) ? step.task.slice(evidenceTask.length) : "";
    const requirement = requirements.find((candidate) => candidate.runs.some((candidateRun) => candidateRun.id === run));
    return run !== "" && requirement !== undefined ? `${requirement.id} judge` : step.agent;
  }
  if (step.task !== "") {
    return `${step.task} ${step.readOnly ? "review" : "implement"}`;
  }
  return step.agent;
}

// sessionNameOf names a session. The steerer reads steering. A session a step holds reads that step's name. A session no
// step holds reads its label as the epic lists it, and else the first 8 characters of its id.
function sessionNameOf(
  session: UsageEpicSession,
  steps: readonly EpicStep[],
  requirements: readonly EpicRequirement[],
  epicSessions: readonly EpicSession[],
): string {
  if (session.shared) {
    return "steering";
  }
  const step = steps.find((candidate) => candidate.id === session.session);
  if (step !== undefined) {
    return stepNameOf(step, requirements);
  }
  const label = epicSessions.find((candidate) => candidate.id === session.session)?.label ?? "";
  return label !== "" ? label : session.session.slice(0, 8);
}

// UsageRow is one session of the epic as a row: its name, its model, its summed requests, cache write tokens and list cost in
// nano US dollars, and whether it is shared.
interface UsageRow {
  session: string;
  shared: boolean;
  name: string;
  model: string;
  requests: bigint;
  cacheWrite: bigint;
  list: bigint;
}

export function EpicUsageSection({
  sessions,
  steps,
  epicSessions,
  requirements,
  narrow,
}: {
  sessions: readonly UsageEpicSession[];
  steps: readonly EpicStep[];
  epicSessions: readonly EpicSession[];
  requirements: readonly EpicRequirement[];
  narrow: boolean;
}) {
  if (sessions.length === 0) {
    return null;
  }
  const rows: UsageRow[] = sessions
    .map((session) => {
      const sum = sessionSum(session.totals);
      return {
        session: session.session,
        shared: session.shared,
        name: sessionNameOf(session, steps, requirements, epicSessions),
        model: modelRows(session.totals)[0]?.model ?? "",
        requests: sum.requests,
        cacheWrite: sum.cacheWriteTokens,
        list: sum.list,
      };
    })
    .sort((a, b) => {
      if (a.list !== b.list) {
        return a.list > b.list ? -1 : 1;
      }
      return compareText(a.session, b.session);
    });
  const counted = sessions.filter((session) => !session.shared).map((session) => sessionSum(session.totals));
  const listSum = counted.reduce((sum, figures) => sum + figures.list, 0n);
  const billedSum = counted.reduce((sum, figures) => sum + figures.billed, 0n);
  const largest = rows.reduce((max, row) => (row.list > max ? row.list : max), 0n);
  return (
    <section class="epic-usage" aria-label="Usage">
      <header class="sec-head">
        <h3>Usage</h3>
        <span>{`${dollars(listSum)} at list · billed ${dollars(billedSum)}`}</span>
      </header>
      <div class={narrow ? "epic-usage-rows narrow" : "epic-usage-rows"}>
        {rows.map((row) => (
          <div class="epic-usage-row" key={row.session}>
            <span class="epic-usage-name">
              <span class="epic-usage-who">{row.name}</span>
              {row.shared && <span class="epic-usage-shared">shared · not in total</span>}
            </span>
            <span class="epic-usage-model">{row.model}</span>
            <span class="epic-usage-requests">{Number(row.requests).toLocaleString("en-US")}</span>
            <span class="epic-usage-cache">{tokens(row.cacheWrite)}</span>
            <span class="epic-usage-list">{dollars(row.list)}</span>
            {narrow ? null : (
              <span class="epic-usage-bar">
                <i style={{ width: `${largest === 0n ? 0 : (Number(row.list) / Number(largest)) * 100}%` }} />
              </span>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}

import type { JSX } from "preact";

import type { CellTone, ReleaseView } from "./ending";

type Step = ReleaseView["rows"][number];

// glyphs are the glyphs of a state cell, by its tone: a passed step is ticked and a failed one crossed.
const glyphs: Record<CellTone, string> = { none: "", live: "", ok: "✓", bad: "✗" };

// ReleaseTable draws an epic's release steps as one table: each step its position, its text and target, its undo and its
// state. Under a step, its reason, evidence and runs. The Then line is drawn under the table.
export function ReleaseTable(props: { view: ReleaseView }): JSX.Element {
  const { view } = props;
  return (
    <>
      <div class="tbl release">
        <div class="th">
          <span />
          <span>step</span>
          <span>undo</span>
          <span />
        </div>
        {view.rows.map((step) => (
          <StepRow key={step.position} step={step} />
        ))}
      </div>
      {view.then !== "" && <p class="inote then">{view.then}</p>}
    </>
  );
}

// StepRow is one step: its row, and under it its detail while the step has one.
function StepRow(props: { step: Step }): JSX.Element {
  const { step } = props;
  const hasDetail = step.reason !== "" || step.evidence !== "" || step.runs.length > 0;
  return (
    <>
      <div class="tr" data-step={step.position}>
        <span class="id">{step.position}</span>
        <span class="wide">
          <span class="strong">{step.step}</span>
          {step.target !== "" && <span class="to">{` → ${step.target}`}</span>}
        </span>
        <span class="undo">{step.undo}</span>
        <span class="end">
          <span class={`cell ${step.tone}`}>
            {glyphs[step.tone] !== "" && <span aria-hidden="true">{glyphs[step.tone]}</span>}
            {step.state}
          </span>
        </span>
      </div>
      {hasDetail && (
        <div class="tr-detail">
          {step.reason !== "" && <p class="inote error">{step.reason}</p>}
          {step.evidence !== "" && <div class="log">{step.evidence}</div>}
          {step.runs.map((run) => (
            <p class="run">{run}</p>
          ))}
        </div>
      )}
    </>
  );
}

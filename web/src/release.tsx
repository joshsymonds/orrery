import type { JSX } from "preact";
import { useEffect } from "preact/hooks";

import type { CellTone, ReleaseView } from "./ending";
import { type CallOutcome, type OwnerCall, useOwnerCall } from "./ownercall";
import { Sheet } from "./sheet";

type Step = ReleaseView["rows"][number];

// glyphs are the glyphs of a state cell, by its tone: a passed step is ticked and a failed one crossed.
const glyphs: Record<CellTone, string> = { none: "", live: "", ok: "✓", bad: "✗" };

// ReleaseTable draws an epic's release steps as one table: each step its position, its text and target, its undo and its
// state. Under a step, its reason, evidence and runs. The offered step carries Release from step N, which asks first and
// on confirm sends one release of that step through onRelease. narrow asks in a sheet, not in place. reread reads the
// epic again after a call that answered, or may have gone through.
export function ReleaseTable(props: {
  view: ReleaseView;
  narrow: boolean;
  onRelease: (undoNone: number[]) => Promise<CallOutcome>;
  reread: () => Promise<boolean>;
}): JSX.Element {
  const { view, narrow, onRelease, reread } = props;
  const call = useOwnerCall(reread);
  // A confirm closes when the view stops offering its step: nothing is sent, and the step is at rest when offered again.
  // The call's line stays: under the offered step's control, else under the table.
  useEffect(() => {
    if (call.phase === "asking" && call.asking !== String(view.offer)) {
      call.keep();
    }
  }, [view.offer]);
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
          <StepRow
            key={step.position}
            step={step}
            offered={step.position === view.offer}
            narrow={narrow}
            call={call}
            onRelease={onRelease}
          />
        ))}
      </div>
      {!view.rows.some((step) => step.position === view.offer) && <CallLine call={call} />}
      {view.then !== "" && <p class="inote then">{view.then}</p>}
    </>
  );
}

// StepRow is one step: its row, and under it its detail while the step has one.
function StepRow(props: {
  step: Step;
  offered: boolean;
  narrow: boolean;
  call: OwnerCall;
  onRelease: (undoNone: number[]) => Promise<CallOutcome>;
}): JSX.Element {
  const { step, offered, narrow, call, onRelease } = props;
  const hasDetail = step.reason !== "" || step.evidence !== "" || step.runs.length > 0 || offered;
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
          {offered && <ReleaseFrom step={step} narrow={narrow} call={call} onRelease={onRelease} />}
        </div>
      )}
    </>
  );
}

// ReleaseFrom is the Release from step N control, and while it is asked, its confirm: in place on the desk, in a sheet
// when narrow. The call's line is drawn under the control.
function ReleaseFrom(props: {
  step: Step;
  narrow: boolean;
  call: OwnerCall;
  onRelease: (undoNone: number[]) => Promise<CallOutcome>;
}): JSX.Element {
  const { step, narrow, call, onRelease } = props;
  const label = `Release from step ${step.position}`;
  const question = `${label}?`;
  const line = `Step ${step.position}, ${step.step}, cannot be undone.`;
  const asking = call.phase === "asking" && call.asking === String(step.position);
  const confirm = () => call.confirm(() => onRelease([step.position]));
  return (
    <>
      {asking && !narrow ? (
        <>
          <span class="sure">{question}</span>
          <span class="sure">{line}</span>
          <button type="button" class="act primary" onClick={confirm}>
            {label}
          </button>
          <button type="button" class="act" onClick={call.keep}>
            Keep it stopped
          </button>
        </>
      ) : (
        <button type="button" class="act" disabled={call.disabled} onClick={() => call.ask(String(step.position))}>
          {label}
        </button>
      )}
      {asking && narrow && (
        <Sheet title={question} onClose={call.keep}>
          <p>{line}</p>
          <div class="foot">
            <button type="button" class="act" onClick={call.keep}>
              Keep it stopped
            </button>
            <button type="button" class="act danger" onClick={confirm}>
              {label}
            </button>
          </div>
        </Sheet>
      )}
      <CallLine call={call} />
    </>
  );
}

// CallLine is the call's line, in the bad role for a refusal, and Read it again while the read after a call has failed.
function CallLine(props: { call: OwnerCall }): JSX.Element {
  const { call } = props;
  return (
    <>
      {call.line !== "" && <p class={call.refused ? "inote call-line error" : "inote call-line"}>{call.line}</p>}
      {call.phase === "unread" && (
        <button type="button" class="act" onClick={call.readAgain}>
          Read it again
        </button>
      )}
    </>
  );
}

import type { JSX } from "preact";
import { useEffect, useState } from "preact/hooks";

import type { ReportView } from "./ending";
import { type CallOutcome, useOwnerCall } from "./ownercall";
import { Sheet } from "./sheet";

type CloseKind = "superseded" | "shipped" | "abandoned";

// closeWords names a kind on Close as…'s confirm.
const closeWords: Record<CloseKind, string> = { superseded: "superseded", shipped: "shipped", abandoned: "abandoned" };

// EpicReport draws an epic's Report section, headed Report: its outcome sentence, why, what shipped and what did not, and the epic that replaced
// it. When the epic ended with gaps it also draws the two owner calls that answer them, Release it anyway and Close as….
// Both share one owner call: each asks first, sends once when confirmed, and says how the call answered. At the desk the
// confirm replaces its control in place; when narrow it is a sheet.
export function EpicReport(props: {
  view: ReportView;
  slug: string;
  narrow: boolean;
  replacements: { id: string; slug: string }[];
  onRelease: (undoNone: number[]) => Promise<CallOutcome>;
  onClose: (kind: CloseKind, by: string) => Promise<CallOutcome>;
  reread: () => Promise<boolean>;
}): JSX.Element {
  const { view, slug, narrow, replacements, onRelease, onClose, reread } = props;
  const call = useOwnerCall(reread);
  // kind and by are Close as…'s pick. They are set while its confirm is open, and a new Close as… starts with none.
  const [kind, setKind] = useState<CloseKind | "">("");
  const [by, setBy] = useState("");
  // gone is whether the epic picked is no longer listed: Close as… shows its line until the owner picks again.
  const [gone, setGone] = useState(false);
  // sure is the control whose confirm is open: asking, or sending the call it asked.
  const sure = call.phase === "asking" || call.phase === "sending" ? call.asking : "";
  // closePick is the call Close as… sends: its kind and, for superseded, the epic picked. Null until the pick is complete.
  const closePick =
    kind === "" || (kind === "superseded" && by === "") ? null : { kind, by: kind === "superseded" ? by : "" };
  // pickGone is whether the epic picked for superseded is no longer among the listed replacements.
  const pickGone = kind === "superseded" && by !== "" && !replacements.some((replacement) => replacement.id === by);
  const dropPick = () => {
    setKind("");
    setBy("");
    setGone(true);
  };
  // A pick that the listed epics drop is cleared while Close as… is open, and the confirm stays disabled until the
  // owner picks again.
  useEffect(() => {
    if (pickGone) {
      dropPick();
    }
  }, [pickGone]);
  // A release or close the owner asked for closes, sending nothing, when the epic is no longer ended with gaps, the
  // same as keep does. Offered again, it is at rest.
  useEffect(() => {
    if (!view.gaps && call.phase === "asking") {
      call.keep();
    }
  }, [view.gaps, call.phase]);
  const askClose = () => {
    if (call.phase === "rest") {
      setKind("");
      setBy("");
      setGone(false);
    }
    call.ask("close");
  };
  const closeLabel = kind === "" ? "Close as…" : `Close as ${closeWords[kind]}`;
  const choices = (
    <div class="choices">
      <label>
        <input
          type="radio"
          name="close-kind"
          value="superseded"
          disabled={replacements.length === 0}
          checked={kind === "superseded"}
          onChange={() => {
            setKind("superseded");
            setGone(false);
          }}
        />{" "}
        {replacements.length === 0 ? "no other epic in this repository" : "superseded by"}
      </label>
      {replacements.length > 0 && (
        <select
          value={by}
          onChange={(event) => {
            setBy(event.currentTarget.value);
            setKind("superseded");
            setGone(false);
          }}
        >
          <option value="">pick the epic</option>
          {replacements.map((replacement) => (
            <option key={replacement.id} value={replacement.id}>
              {replacement.slug}
            </option>
          ))}
        </select>
      )}
      <label>
        <input
          type="radio"
          name="close-kind"
          value="shipped"
          checked={kind === "shipped"}
          onChange={() => {
            setKind("shipped");
            setGone(false);
          }}
        />{" "}
        shipped outside orrery
      </label>
      <label>
        <input
          type="radio"
          name="close-kind"
          value="abandoned"
          checked={kind === "abandoned"}
          onChange={() => {
            setKind("abandoned");
            setGone(false);
          }}
        />{" "}
        abandoned
      </label>
    </div>
  );
  const releaseLine = "Its gaps stay, and its release steps run.";
  const confirmRelease = () => call.confirm(() => onRelease([]));
  const confirmClose = () => {
    if (closePick === null) {
      return;
    }
    // A pick the list dropped before this press is not sent: it is cleared, and the dialog says so.
    if (pickGone) {
      dropPick();
      return;
    }
    call.confirm(() => onClose(closePick.kind, closePick.by));
  };
  const goneLine = gone ? (
    <p class="inote error close-gone">The epic you picked is no longer listed. Choose again.</p>
  ) : null;
  const releaseControl = (
    <button type="button" class="act" disabled={call.disabled} onClick={() => call.ask("release")}>
      Release it anyway
    </button>
  );
  const closeControl = (
    <button type="button" class="act" disabled={call.disabled} onClick={askClose}>
      Close as…
    </button>
  );
  return (
    <section class="report" aria-label="Report">
      <header class="sec-head">
        <h3>Report</h3>
      </header>
      <p class="now-sentence">{view.sentence}</p>
      {view.why !== "" && <p class="report-why">{view.why}</p>}
      {(view.shipped !== "" || view.notShipped !== "") && (
        <div class="doors">
          {view.shipped !== "" && (
            <span class="door shipped">
              <span>shipped</span> <span>{view.shipped}</span>
            </span>
          )}
          {view.notShipped !== "" && (
            <span class="door not-shipped">
              <span>not shipped</span> <span>{view.notShipped}</span>
            </span>
          )}
        </div>
      )}
      {view.replacedBy !== "" && <p class="replaced">Superseded by {view.replacedBy}</p>}
      {view.gaps && (
        <div class="gaps-acts">
          {!narrow && sure === "release" ? (
            <>
              <span class="sure">{`Release ${slug} anyway?`}</span>
              <span class="sure">{releaseLine}</span>
              <button type="button" class="act primary" disabled={call.disabled} onClick={confirmRelease}>
                Release it anyway
              </button>
              <button type="button" class="act" disabled={call.disabled} onClick={call.keep}>
                Keep it
              </button>
            </>
          ) : (
            releaseControl
          )}
          {!narrow && sure === "close" ? (
            <>
              <span class="sure">{`Close ${slug} as…`}</span>
              {choices}
              {goneLine}
              <button
                type="button"
                class="act primary"
                disabled={call.disabled || closePick === null}
                onClick={confirmClose}
              >
                {closeLabel}
              </button>
              <button type="button" class="act" disabled={call.disabled} onClick={call.keep}>
                Keep it
              </button>
            </>
          ) : (
            closeControl
          )}
          {call.line !== "" && <p class={call.refused ? "inote call-line error" : "inote call-line"}>{call.line}</p>}
          {call.phase === "unread" && (
            <button type="button" class="act" onClick={call.readAgain}>
              Read it again
            </button>
          )}
        </div>
      )}
      {narrow && view.gaps && sure === "release" && (
        <Sheet title={`Release ${slug} anyway?`} onClose={call.keep}>
          <p>{releaseLine}</p>
          <div class="foot">
            <button type="button" class="act" disabled={call.disabled} onClick={call.keep}>
              Keep it
            </button>
            <button type="button" class="act danger" disabled={call.disabled} onClick={confirmRelease}>
              Release it anyway
            </button>
          </div>
        </Sheet>
      )}
      {narrow && view.gaps && sure === "close" && (
        <Sheet title={`Close ${slug} as…`} onClose={call.keep}>
          {choices}
          {goneLine}
          <div class="foot">
            <button type="button" class="act" disabled={call.disabled} onClick={call.keep}>
              Keep it
            </button>
            <button
              type="button"
              class="act danger"
              disabled={call.disabled || closePick === null}
              onClick={confirmClose}
            >
              {closeLabel}
            </button>
          </div>
        </Sheet>
      )}
    </section>
  );
}

import type { JSX } from "preact";
import { useEffect, useState } from "preact/hooks";

import { type AskModel, type AskOption, staleWords } from "./askmodel";
import { Ctl } from "./ctl";
import { Icon } from "./glyphs";
import { ageText } from "./inboxrow";
import { Menu, type MenuEntry } from "./menu";
import { Sheet } from "./sheet";

// AskPhase is where a session's or pane's answer stands: open, sent and waiting for the stream, or refused.
export type AskPhase = "open" | "sending" | "error";

export interface AskProps {
  model: AskModel;
  now: number;
  phase: AskPhase;
  picked: number | null;
  note: string;
  keyCaps: boolean;
  narrow: boolean;
  numberKeys: boolean;
  pick?: { choice: string; text: string };
  onAnswer?: (n: number) => void;
  onNotNow?: () => void;
  onBringBack?: () => void;
  onDecline?: () => void;
  onArchive?: () => void;
  onConfirm?: (choice: string, text: string) => Promise<string>;
}

// answerFailedLine is an epic answer's words when its call fails without a message: the answer is kept, so it can be sent again.
const answerFailedLine = "Not answered. Your answer is kept.";

// lastKey is the last number a key picks; an option past it is drawn with no key cap and no button.
const lastKey = 9;

const notNowTip =
  "Take it off your list. Nothing is sent: it stays waiting here, and anything new it asks comes back.";

// isTextBox reports whether an element is a text box, whose keys are text and no command.
function isTextBox(element: Element | null): boolean {
  return element instanceof HTMLElement && (element.tagName === "TEXTAREA" || element.tagName === "INPUT");
}

// initialPick is the option an epic's Ask starts with: the pick it was given, else the recommended option, else none.
function initialPick(model: AskModel, given: AskProps["pick"]): number | null {
  if (given !== undefined) {
    const index = given.choice === "other" ? model.options.length : model.options.findIndex((option) => option.name === given.choice);
    return index < 0 ? null : index;
  }
  const recommended = model.options.findIndex((option) => option.recommended);
  return recommended < 0 ? null : recommended;
}

// Ask draws one open question as its owner sees it: its head with the controls that answer, park or decline it, its
// question and options, the foot that sends the answer, and the notes. A session's or pane's Ask holds no answer of its
// own: its phase, picked option and note come in from the App, and a tap calls onAnswer. An epic's Ask keeps its pick and
// its text here, and answers only on confirm, which calls onConfirm and shows the words it returns.
export function Ask(props: AskProps): JSX.Element {
  const { model, now, keyCaps, narrow, numberKeys, onAnswer, onNotNow, onBringBack, onDecline, onArchive, onConfirm } =
    props;
  const epic = model.epic !== null;
  // The question's Something else is the option after the last one; a park has none.
  const otherIndex = model.options.length;
  const hasOther = model.kind === "epic";
  const pickCount = otherIndex + (hasOther ? 1 : 0);
  const caps = keyCaps && !narrow;

  const [picked, setPicked] = useState<number | null>(() => initialPick(model, props.pick));
  const [words, setWords] = useState(props.pick?.choice === "other" ? props.pick.text : "");
  const [answering, setAnswering] = useState(false);
  const [failure, setFailure] = useState("");
  const [more, setMore] = useState(false);

  const sending = epic ? answering : props.phase === "sending";
  const note = epic ? failure : props.note;
  const putOff = model.putOff;

  // A digit key picks the option it names on an epic's page, unless a text box has focus. It never confirms.
  useEffect(() => {
    if (!numberKeys || !epic) {
      return undefined;
    }
    const listener = (event: KeyboardEvent) => {
      if (event.ctrlKey || event.metaKey || event.altKey || isTextBox(document.activeElement)) {
        return;
      }
      if (/^[1-9]$/.test(event.key) && Number(event.key) <= pickCount) {
        setPicked(Number(event.key) - 1);
      }
    };
    document.addEventListener("keydown", listener);
    return () => document.removeEventListener("keydown", listener);
  }, [numberKeys, epic, pickCount]);

  // confirm sends an epic's pick: its choice's id with what it does, or other with the trimmed text.
  async function confirm(): Promise<void> {
    if (picked === null || sending || onConfirm === undefined) {
      return;
    }
    const sent = words;
    const isOther = picked === otherIndex && hasOther;
    const choice = isOther ? "other" : model.options[picked].name;
    const text = isOther ? sent.trim() : model.options[picked].does;
    setAnswering(true);
    setFailure("");
    let result: string;
    try {
      result = await onConfirm(choice, text);
    } catch {
      result = answerFailedLine;
    }
    setAnswering(false);
    setFailure(result);
    if (result === "") {
      // Text typed while the answer was in flight stays in the box.
      setWords((current) => (current === sent ? "" : current));
    }
  }

  // answerSession sends a session's or pane's option. A press while an answer is in flight sends nothing.
  function answerSession(option: AskOption): void {
    if (sending || onAnswer === undefined) {
      return;
    }
    onAnswer(option.n);
  }

  const declineItems: MenuEntry[] = [
    {
      label: "Decline and end the turn",
      icon: "stop",
      sub: "denies the request",
      danger: true,
      onSelect: () => {
        setMore(false);
        onDecline?.();
      },
    },
  ];

  // A put-off question is quiet: it keeps its asked age, and draws no stale foot.
  const stale = putOff ? null : staleWords(model.asked, now);
  const age = ageText(model.asked, now);
  const pickedWords = picked === null ? "" : picked === otherIndex && hasOther ? "Something else" : model.options[picked].name;
  const recordText = picked === null ? "" : picked === otherIndex && hasOther ? words.trim() : model.options[picked].does;

  return (
    <section class={putOff ? "ask later" : "ask"} aria-label="a question for you">
      <div class="head">
        <span class="lbl">{putOff ? "Put off" : "Asks you"}</span>
        <span class="cut">{model.who}</span>
        {age !== "" && <span class="age">· {age} ago</span>}
        <span class="end">
          {model.kind === "permission" && <span class="chip attention">permission</span>}
          {!epic &&
            !sending &&
            (putOff
              ? onBringBack !== undefined && <Ctl label="Bring it back" icon="retry" tip="Back on your list" onClick={onBringBack} />
              : onNotNow !== undefined && <Ctl label="Not now" icon="clock" tip={notNowTip} onClick={onNotNow} />)}
          {!epic && !sending && onDecline !== undefined && (
            <Ctl label="More for this question" only icon="more" expanded={more} onClick={() => setMore(!more)} />
          )}
          {more && !narrow && !epic && !sending && <Menu items={declineItems} />}
        </span>
      </div>
      <p class="q">{model.question}</p>
      {model.command !== null && (
        <figure class="cmd">
          <figcaption class="t-label">{model.command.tool}</figcaption>
          <pre>
            <code>{model.command.text}</code>
          </pre>
        </figure>
      )}
      {!epic && model.options.length === 0 && (
        <p class="inote">This question cannot be answered from here. Reply in words below.</p>
      )}
      {(model.options.length > 0 || hasOther) && (
        <div class="opts">
          {model.options.map((option, index) => {
            const held = epic ? picked === index : props.picked === option.n;
            const inner = (
              <>
                {caps && option.answerable && <kbd>{option.n}</kbd>}
                <span class="nm">{option.name}</span>
                {option.recommended && <span class="chip">recommended</span>}
                {option.does !== "" && <span class="ds">{option.does}</span>}
                {option.why !== "" && <span class="ds">Why: {option.why}.</span>}
              </>
            );
            const pressable = option.answerable && (epic || onAnswer !== undefined);
            if (!pressable) {
              return (
                <div key={index} class={held ? "opt picked" : "opt"}>
                  {inner}
                </div>
              );
            }
            return (
              <button
                key={index}
                type="button"
                class={held ? "opt picked" : "opt"}
                aria-pressed={held}
                onClick={() => {
                  if (epic) {
                    if (!sending) {
                      setPicked(index);
                    }
                  } else {
                    answerSession(option);
                  }
                }}
              >
                {inner}
              </button>
            );
          })}
          {hasOther && (
            <div
              role="button"
              tabIndex={0}
              class={picked === otherIndex ? "opt picked" : "opt"}
              aria-pressed={picked === otherIndex}
              onClick={() => {
                if (!sending) {
                  setPicked(otherIndex);
                }
              }}
              onKeyDown={(event) => {
                // A key in the text box is typing, not a pick of the option around it.
                if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) {
                  return;
                }
                event.preventDefault();
                if (!sending) {
                  setPicked(otherIndex);
                }
              }}
            >
              {caps && otherIndex + 1 <= lastKey && <kbd>{otherIndex + 1}</kbd>}
              <span class="nm">Something else</span>
              <span class="ds">Type an answer for the planner.</span>
              {picked === otherIndex && (
                <div class="field area">
                  <textarea
                    aria-label="answer"
                    placeholder="Your answer"
                    value={words}
                    onInput={(event) => setWords(event.currentTarget.value)}
                  />
                </div>
              )}
            </div>
          )}
        </div>
      )}
      {epic ? (
        <div class="foot">
          {onConfirm !== undefined && (
            <button
              type="button"
              class="btn primary"
              disabled={picked === null || sending || (picked === otherIndex && hasOther && words.trim() === "")}
              onClick={() => void confirm()}
            >
              {pickedWords === "" ? "Answer" : `Answer with ${pickedWords}`}
            </button>
          )}
          {sending && <span class="inote">sending…</span>}
          {hasOther && picked !== null && recordText !== "" && (
            <div class="record grow">
              <span class="t-label">Will be recorded</span>
              <code>{`owner: ${recordText}`}</code>
            </div>
          )}
        </div>
      ) : (
        sending && (
          <div class="foot">
            <span class="inote">sending…</span>
          </div>
        )
      )}
      {note !== "" && (
        <p class="inote error" role="alert">
          <Icon name="warn" />
          <span>{note}</span>
        </p>
      )}
      {stale !== null && (
        <div class="foot">
          <span class="inote">Asked {stale} ago and still waiting.</span>
          {model.archive !== null && onArchive !== undefined && (
            <button type="button" class="btn quiet" onClick={() => onArchive()}>
              <Icon name="archive" />
              <span>Archive this session</span>
              {caps && <kbd>A</kbd>}
            </button>
          )}
        </div>
      )}
      {more && narrow && !epic && !sending && onDecline !== undefined && (
        <Sheet title="More for this question" onClose={() => setMore(false)}>
          <Menu items={declineItems} />
        </Sheet>
      )}
    </section>
  );
}

export type AskLineProps =
  | { state: "answered"; n: number; name: string; question: string; keyCaps: boolean }
  | { state: "settled"; who: string };

// AskLine draws an Ask once it is no longer open, as one line: what was answered, or that an epic's question is settled.
export function AskLine(props: AskLineProps): JSX.Element {
  if (props.state === "settled") {
    return (
      <div class="ask folded settled">
        <span class="lbl">Settled</span>
        <span>This question is settled.</span>
        <span class="ink-3">· {props.who}</span>
      </div>
    );
  }
  return (
    <div class="ask folded">
      <span class="lbl">You answered</span>
      {props.keyCaps && <kbd>{props.n}</kbd>}
      <span>{props.name}</span>
      <span class="ink-3">· {props.question}</span>
    </div>
  );
}

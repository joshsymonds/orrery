import { Fragment } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import * as actions from "./actions";
import type { Clients } from "./actions";
import { Composer } from "./composer";
import type { Conversation } from "./conversation";
import {
  type EpicItem,
  type EpicSection,
  type EpicsView,
  noEpics,
  placeEpics,
  type StepItem,
  withEpicsGroup,
} from "./epics";
import { EpicView } from "./epicview";
import { type Followed, follow } from "./follow";
import { Mark } from "./mark";
import { type ParsedQuestion, parseQuestion } from "./question";
import { Reader, ReaderHead } from "./reader";
import { byAttention, byProject, type Row } from "./rows";
import { type SeenClock, type SeenWindow, watchSeen } from "./seen";
import { StepBox } from "./stepbox";
import { ReplyText } from "./text";
import { Pill, rowPill, stepPill, WorkNote } from "./workline";

type View = "attention" | "project";

// The conversation the reader holds, with the row key it belongs to, so a row never shows another row's transcript.
interface Stream {
  key: string;
  conversation: Conversation;
}

// The cursor is held by entry key, so it survives new entries. index is where it sat, used when its entry leaves.
interface Cursor {
  key: string;
  index: number;
}

// A landed answer from the list: the choice and its label, kept for the row and the question it answered.
interface Answer {
  question: string;
  choice: number;
  label: string;
}

// An entry the list shows and the cursor can rest on: a row, or an epic or step of the Epics group.
type Item = Row | EpicItem | StepItem;

const stateWaiting = "waiting";
const stateWorking = "working";
const needsYouTitle = "Needs you";
const newSinceTitle = "New since you looked";
const noDirectoryTitle = "no directory";
const tasksUnavailableText = "tasks unavailable";
const epicUnavailableText = "epic unavailable";
const seenFailedText = "could not mark seen";
const answerFailedText = "no answer was sent";
const heldMark = "‖";

// The keys 1-3 answer the first three options of a question, and no others.
const keyedOptions = 3;

// A task strip past this many segments draws dense, its segments touching.
const denseStrip = 60;

const secondsPerMinute = 60;
const minutesPerHour = 60;
const hoursPerDay = 24;

// A step keeps no seen mark. Infinity makes no item newer than it, so the step's reader draws no since-you-looked rule.
const stepSeenAt = Number.POSITIVE_INFINITY;

const helpEntries: [string[], string][] = [
  [["j", "k", "↓", "↑"], "move the cursor one row up or down"],
  [["gg"], "first row"],
  [["G"], "last row"],
  [["a"], "by attention"],
  [["p"], "by project"],
  [["w"], "first waiting row"],
  [["n"], "next unseen row, wrapping"],
  [["x", "Enter"], "mark the cursor's row seen"],
  [["e"], "mark it seen, then go to the next unseen row"],
  [["/"], "filter: Enter leaves it, Esc clears it"],
  [["i"], "focus the reply box"],
  [["1", "2", "3"], "answer the cursor's waiting question with that choice"],
  [["o"], "open or close a compaction's summary"],
  [["|"], "pin the cursor's row in a second column, or close the pin"],
  [["?"], "toggle this help"],
  [["Esc"], "close this help"],
];

export function App({
  rows,
  error,
  now,
  clients,
  seenClock,
  seenWindow,
  epics = noEpics,
}: {
  rows: Row[];
  error: string;
  now: number;
  clients: Clients;
  seenClock?: SeenClock;
  seenWindow?: SeenWindow;
  epics?: EpicsView;
}) {
  const [view, setView] = useState<View>("attention");
  const [cursor, setCursor] = useState<Cursor>({ key: "", index: 0 });
  const [filter, setFilter] = useState("");
  const [helpOpen, setHelpOpen] = useState(false);
  const [open, setOpen] = useState(false);
  const [stream, setStream] = useState<Stream | null>(null);
  // The rows the cursor has rested on, each with its own composer, so a draft waits where it was typed. A row stays
  // here while it is missing from the list, as it is for a poll whose list call failed, so its draft is not lost.
  const [visited, setVisited] = useState<Row[]>([]);
  // The steps the cursor has rested on, each with its own step box, kept as the visited rows are so a draft survives.
  const [visitedSteps, setVisitedSteps] = useState<StepItem[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);
  const composerInputs = useRef(new Map<string, HTMLTextAreaElement>());
  const stepInputs = useRef(new Map<string, HTMLTextAreaElement>());
  // The row | pinned beside the list, with its own stream. The rows that have been pinned each keep a composer, as the
  // visited rows do, so a draft or a failed send survives the pin's close and its reopening on that row.
  const [pinned, setPinned] = useState<Row | null>(null);
  const [pinStream, setPinStream] = useState<Stream | null>(null);
  const [pinnedRows, setPinnedRows] = useState<Row[]>([]);
  const pinnedInputs = useRef(new Map<string, HTMLTextAreaElement>());
  const pendingG = useRef(false);
  const handler = useRef<(event: KeyboardEvent) => void>();
  // The changed time each row had when its seen write succeeded. A row reads as seen until its changed time moves past it.
  const [seenWritten, setSeenWritten] = useState<Map<string, number>>(() => new Map());
  const [seenError, setSeenError] = useState("");
  const [answerError, setAnswerError] = useState("");
  // The answers that landed from the list, by row key. Each shows in place of its row's chips while the row
  // waits on the question it answered.
  const [answered, setAnswered] = useState<Map<string, Answer>>(() => new Map());
  const seenWatch = useRef<ReturnType<typeof watchSeen>>();
  // The row the seen watch last rested on, which its write marks seen.
  const restedRow = useRef<Row | null>(null);

  const words = filter
    .toLowerCase()
    .split(/\s+/)
    .filter((word) => word !== "");
  const shownRows = rows.map((row) => readAsSeen(row, seenWritten));
  // The brand's counts read every loaded row as the list reads it after seen writes, before the filter.
  const waitingCount = shownRows.filter((row) => row.state === stateWaiting).length;
  const workingCount = shownRows.filter((row) => row.state === stateWorking).length;
  const newCount = shownRows.filter((row) => row.unseen).length;
  // The footer names the hosts the loaded rows come from, each once, in the order the rows first show them.
  const hosts = [...new Set(rows.map((row) => row.host))].filter((host) => host !== "");
  const matching = shownRows.filter((row) => matchesFilter(row, words));
  const sections =
    view === "attention"
      ? withEpicsGroup(byAttention(matching), epics)
      : placeEpics(byProject(matching), epics.blocks);
  const flat = sections.flatMap(entriesOf);
  const index = resolveIndex(flat, cursor);
  const cursorItem = index >= 0 ? flat[index] : null;
  // The cursor's row, which an epic or step entry never has: the composer and seen watch follow only a row.
  const cursorRow = cursorItem !== null && isRow(cursorItem) ? cursorItem : null;
  const cursorKey = cursorItem?.key ?? "";
  const cursorRowKey = cursorRow?.key ?? "";
  // The cursor's step, whose reader follows its session and whose step box it shows. An epic entry is no step.
  const cursorStep = cursorItem?.kind === "step" ? cursorItem : null;
  const cursorStepKey = cursorStep?.key ?? "";
  // The entry the reader follows: the cursor's row or step. Its stream is kept under this key, so a stream never
  // shows under another entry.
  const readerKey = cursorRowKey !== "" ? cursorRowKey : cursorStepKey;
  // The cursor's epic or step entry, which the right column draws whether or not a row is pinned. A step entry draws
  // the epic it belongs to, with the step marked; an epic entry marks no step.
  const cursorEpic = cursorItem !== null && !isRow(cursorItem) ? cursorItem : null;
  const cursorStepId = cursorEpic?.kind === "step" ? cursorEpic.id : "";
  const stepEntries = flat.filter((item): item is StepItem => item.kind === "step");
  const epicDetail = cursorEpic === null ? undefined : epics.details.get(epicIdOf(cursorEpic));
  const shownIndex = Math.max(index, 0);

  // The held cursor follows the entry shown, so a first render or a clamp does not leave it on a stale key.
  useEffect(() => {
    if (cursor.key !== cursorKey || cursor.index !== shownIndex) {
      setCursor({ key: cursorKey, index: shownIndex });
    }
  }, [cursor, cursorKey, shownIndex]);

  // The cursor's row or step is followed while the cursor rests on it: a move to another entry, or an unmount, stops the
  // stream. A step is followed as its session, by its id.
  useEffect(() => {
    let target: Followed | null = null;
    if (cursorRow !== null) {
      target = { kind: cursorRow.kind, id: cursorRow.id };
    } else if (cursorStep !== null) {
      target = { kind: "session", id: cursorStep.id };
    }
    if (target === null) {
      return undefined;
    }
    return follow(clients, target, (conversation) => setStream({ key: readerKey, conversation }));
  }, [readerKey, clients]);

  useEffect(() => {
    setVisited((previous) => keepEntries(previous, rows, cursorRow));
  }, [rows, cursorRowKey]);

  useEffect(() => {
    setVisitedSteps((previous) => keepEntries(previous, stepEntries, cursorStep));
  }, [epics, cursorStepKey]);

  useEffect(() => {
    setPinnedRows((previous) => keepEntries(previous, rows, pinned));
  }, [rows, pinned]);

  // An answer's line goes when its row stops waiting or waits on another question. A row missing from the list keeps
  // it, as a failed poll leaves a pane out of the list.
  useEffect(() => {
    setAnswered((previous) => {
      const kept = new Map([...previous].filter(([key, answer]) => keepsAnswer(rows, key, answer)));
      return kept.size === previous.size ? previous : kept;
    });
  }, [rows]);

  // The pinned row is followed while it stays pinned: its close, or an unmount, stops the stream.
  const pinnedKey = pinned?.key ?? "";
  useEffect(() => {
    if (pinned === null) {
      return undefined;
    }
    return follow(clients, { kind: pinned.kind, id: pinned.id }, (conversation) =>
      setPinStream({ key: pinned.key, conversation }),
    );
  }, [pinnedKey, clients]);

  // A pinned row keeps its last shape while it is missing from the list, as a visited row does.
  useEffect(() => {
    setPinned((previous) => (previous === null ? null : (rows.find((row) => row.key === previous.key) ?? previous)));
  }, [rows]);

  // writeSeen marks a row seen through actions.markSeen. A success reads the row as seen; a failure leaves it unseen and
  // shows a fixed line.
  const writeSeen = async (row: Row) => {
    try {
      await actions.markSeen(clients, row);
    } catch {
      setSeenError(seenFailedText);
      return;
    }
    setSeenError("");
    setSeenWritten((previous) => new Map(previous).set(row.key, row.changed));
  };

  // answerCursor sends the choice to the cursor's row's waiting question through actions.answer, with the conversation
  // the reader holds for that row only. A false result or a rejection shows a fixed line; nothing is retried. A landed
  // answer names its choice on the row, when the question offers that choice.
  const answerCursor = (choice: 1 | 2 | 3) => {
    if (cursorRow === null) {
      return;
    }
    const row = cursorRow;
    const conversation = stream !== null && stream.key === row.key ? stream.conversation : null;
    const option = readerQuestion(row, conversation)?.options[choice - 1];
    void actions
      .answer(clients, row, conversation, choice)
      .catch(() => false)
      .then((sent) => {
        setAnswerError(sent ? "" : answerFailedText);
        if (sent && option !== undefined) {
          const answer: Answer = { question: row.question, choice, label: optionText(option.label, choice) };
          setAnswered((previous) => new Map(previous).set(row.key, answer));
        }
      });
  };

  // The seen watch runs for the page's life: its write marks the row it last rested on, and unmount stops it.
  useEffect(() => {
    const watch = watchSeen(
      seenWindow ?? window,
      () => {
        const row = restedRow.current;
        if (row !== null) {
          void writeSeen(row);
        }
      },
      seenClock,
    );
    seenWatch.current = watch;
    return () => watch.stop();
  }, [clients, seenClock, seenWindow]);

  // Each render tells the watch the cursor's row and whether it reads as unseen. A changed row is a new key, so a
  // change made while the cursor rests on it gets its own dwell.
  useEffect(() => {
    restedRow.current = cursorRow;
    seenWatch.current?.rest(
      cursorRow === null ? "" : `${cursorRow.key}@${cursorRow.changed}`,
      cursorRow?.unseen ?? false,
    );
  });

  // The conversation the cursor's reader holds, only when its stream belongs to the entry the cursor rests on.
  const cursorConversation = stream !== null && stream.key === readerKey ? stream.conversation : null;
  // The conversation the pinned reader holds, only when its stream belongs to the pinned row.
  const pinnedConversation =
    pinStream !== null && pinned !== null && pinStream.key === pinned.key ? pinStream.conversation : null;
  // The conversation a list row's options come from: the stream the page holds for that row, the cursor's or the pin's.
  const heldConversation = (key: string) =>
    key === cursorRowKey ? cursorConversation : key === pinnedKey ? pinnedConversation : null;
  const toggleOpen = () => setOpen((value) => !value);

  const moveTo = (target: number) => {
    if (flat.length === 0) {
      return;
    }
    const next = clamp(target, flat.length);
    setCursor({ key: flat[next].key, index: next });
  };

  // selectItem moves the cursor to an entry the list shows, as a click on that entry does.
  const selectItem = (item: Item) => {
    moveTo(flat.findIndex((candidate) => candidate.key === item.key));
  };

  const nextUnseen = () => {
    for (let step = 1; step <= flat.length; step++) {
      const target = (index + step) % flat.length;
      const item = flat[target];
      if (isRow(item) && item.unseen) {
        moveTo(target);
        return;
      }
    }
  };

  const handleKey = (event: KeyboardEvent) => {
    if (event.ctrlKey || event.metaKey || event.altKey) {
      return;
    }
    const input = inputRef.current;
    if (input !== null && document.activeElement === input) {
      if (event.key === "Enter") {
        input.blur();
      } else if (event.key === "Escape") {
        setFilter("");
        input.blur();
      }
      return;
    }
    // Keys typed in the reply box are text: only Escape leaves it.
    const composerBoxes = [
      ...composerInputs.current.values(),
      ...pinnedInputs.current.values(),
      ...stepInputs.current.values(),
    ];
    const focusedComposer = composerBoxes.find((box) => box === document.activeElement);
    if (focusedComposer !== undefined) {
      if (event.key === "Escape") {
        focusedComposer.blur();
      }
      return;
    }

    const afterG = pendingG.current;
    pendingG.current = false;
    switch (event.key) {
      case "g":
        if (afterG) {
          moveTo(0);
        } else {
          pendingG.current = true;
        }
        break;
      case "j":
      case "ArrowDown":
        if (index >= 0) {
          moveTo(index + 1);
        }
        break;
      case "k":
      case "ArrowUp":
        if (index >= 0) {
          moveTo(index - 1);
        }
        break;
      case "G":
        moveTo(flat.length - 1);
        break;
      case "a":
        setView("attention");
        break;
      case "p":
        setView("project");
        break;
      case "w": {
        const waiting = flat.findIndex((item) => isRow(item) && item.state === stateWaiting);
        if (waiting >= 0) {
          moveTo(waiting);
        }
        break;
      }
      case "n":
        nextUnseen();
        break;
      case "x":
      case "Enter":
        if (cursorRow !== null) {
          void writeSeen(cursorRow);
        }
        break;
      case "e":
        if (cursorRow !== null) {
          void writeSeen(cursorRow);
          nextUnseen();
        }
        break;
      case "/":
        inputRef.current?.focus();
        break;
      case "i":
        if (cursorStep !== null) {
          stepInputs.current.get(cursorStepKey)?.focus();
        } else {
          composerInputs.current.get(cursorRowKey)?.focus();
        }
        break;
      case "1":
        answerCursor(1);
        break;
      case "2":
        answerCursor(2);
        break;
      case "3":
        answerCursor(3);
        break;
      case "o":
        toggleOpen();
        break;
      case "|":
        // An epic or step entry pins nothing and closes nothing: the pin is a row's.
        if (cursorRow === null && cursorItem !== null) {
          break;
        }
        if (pinned !== null) {
          setPinned(null);
          setPinStream(null);
        } else if (cursorRow !== null) {
          setPinned(cursorRow);
          setPinStream(null);
        }
        break;
      case "?":
        setHelpOpen((open) => !open);
        break;
      case "Escape":
        setHelpOpen(false);
        return;
      default:
        return;
    }
    event.preventDefault();
  };
  handler.current = handleKey;

  useEffect(() => {
    const listener = (event: KeyboardEvent) => handler.current?.(event);
    document.addEventListener("keydown", listener);
    return () => document.removeEventListener("keydown", listener);
  }, []);

  return (
    <main class="app">
      <div class={panesClass(pinned !== null, cursorStep !== null)}>
        <div class="list">
          <header class="top">
            <div class="brand">
              <Mark />
              orrery
              <span class="counts">
                {waitingCount > 0 && <span class="st-waiting">{`${waitingCount} need you`}</span>}
                {workingCount > 0 && <span class="st-working">{`${workingCount} working`}</span>}
                {newCount > 0 && <span class="st-new">{`✱ ${newCount} new`}</span>}
              </span>
            </div>
            <div class="filter">
              <kbd>/</kbd>
              <input
                ref={inputRef}
                value={filter}
                placeholder="Filter"
                aria-label="filter rows"
                onInput={(event) => setFilter(event.currentTarget.value)}
              />
            </div>
            <div class="views">
              <span class={view === "attention" ? "view on" : "view"} onClick={() => setView("attention")}>
                <kbd>a</kbd>By attention
              </span>
              <span class={view === "project" ? "view on" : "view"} onClick={() => setView("project")}>
                <kbd>p</kbd>By project
              </span>
            </div>
            {error !== "" && (
              <p class="error" role="alert">
                {error}
              </p>
            )}
            {seenError !== "" && (
              <p class="error" role="alert">
                {seenError}
              </p>
            )}
            {answerError !== "" && (
              <p class="error" role="alert">
                {answerError}
              </p>
            )}
          </header>
          <div class="rows" data-list={view}>
            {sections.map((section) => (
              <div class="section">
                <div class={sectionClass(section.title)}>
                  <span class="title">{sectionTitle(section)}</span>
                  <i>{section.count}</i>
                </div>
                {section.epics.map((block) => (
                  <Fragment key={block.epic.key}>
                    <EpicEntry
                      epic={block.epic}
                      full={block.full}
                      current={block.epic.key === cursorKey}
                      onSelect={selectItem}
                    />
                    {block.steps.map((step) => (
                      <StepEntry key={step.key} step={step} current={step.key === cursorKey} onSelect={selectItem} />
                    ))}
                    {block.more > 0 && <div class="b-more">{`${block.more} more`}</div>}
                    {block.tail !== "" && <div class="b-more">{block.tail}</div>}
                  </Fragment>
                ))}
                {section.rows.map((item) => (
                  <div
                    key={item.key}
                    class={item.key === cursorRowKey ? "b-row cur" : "b-row"}
                    data-row=""
                    data-key={item.key}
                    data-unseen={item.unseen ? "" : undefined}
                    aria-current={item.key === cursorRowKey ? "true" : undefined}
                    onClick={(event) => {
                      if (!isLinkClick(event)) {
                        selectItem(item);
                      }
                    }}
                    onDblClick={(event) => {
                      if (!isLinkClick(event)) {
                        selectItem(item);
                        void writeSeen(item);
                      }
                    }}
                  >
                    <span class="dot" />
                    <span class={`g g-${item.state}`} aria-hidden="true" />
                    <div class="main">
                      <div class="l1">
                        {item.state === stateWaiting && (
                          <span class="ty">{item.question !== "" ? "QUESTION" : "WAITING"}</span>
                        )}
                        <span class="label">{item.label}</span>
                        <span class="proj">{lastPathElement(item.project)}</span>
                        <span class="age">{ageText(item.changed, now)}</span>
                      </div>
                      <div class={item.unseen ? "l2" : "l2 one"}>
                        <ReplyText
                          text={
                            item.state === stateWaiting && item.question !== ""
                              ? parseQuestion(item.question).title
                              : item.summary
                          }
                        />
                      </div>
                      <RowOptions
                        row={item}
                        conversation={heldConversation(item.key)}
                        answer={answered.get(item.key)}
                      />
                    </div>
                  </div>
                ))}
                {section.folded > 0 && <div class="b-more">{`${section.folded} folded`}</div>}
              </div>
            ))}
          </div>
          <footer class="lfoot">
            {hosts.map((host) => (
              <span key={host} class="host">
                {host}
              </span>
            ))}
            <span class="keys-hint">
              <kbd>?</kbd> keys
            </span>
          </footer>
        </div>
        <section class="read" aria-label="reader" hidden={cursorRow === null && cursorStep === null}>
          {cursorRow !== null && (
            <ReaderHead label={cursorRow.label} where={rowWhere(cursorRow)}>
              <Pill model={rowPill(cursorRow.state, cursorConversation?.state?.pane)} now={now} />
            </ReaderHead>
          )}
          {cursorStep !== null && (
            <ReaderHead label={cursorStep.label} where={stepWhere(cursorStep)}>
              <Pill model={stepPill(cursorStep.held, cursorStep.started)} now={now} />
            </ReaderHead>
          )}
          {cursorRow !== null && (
            <Reader
              conversation={cursorConversation}
              seenAt={cursorRow.seen}
              open={open}
              question={readerQuestion(cursorRow, cursorConversation)}
              onAnswer={answerCursor}
              onToggleOpen={toggleOpen}
            />
          )}
          {cursorStep !== null && (
            <Reader
              conversation={cursorConversation}
              seenAt={stepSeenAt}
              open={open}
              step={cursorStep.step}
              onToggleOpen={toggleOpen}
            />
          )}
          <div class="send">
            <WorkNote pane={cursorConversation?.state?.pane} now={now} />
            {composerRows(visited, cursorRow).map((row) => (
              <div key={row.key} class="reply" hidden={row.key !== cursorRowKey}>
                <Composer
                  label={row.label}
                  send={(text, promptId) => actions.send(clients, row, text, promptId)}
                  pull={row.kind === "pane" ? () => actions.pullQueued(clients, row) : null}
                  newPromptId={actions.newPromptId}
                  queued={row.key === cursorRowKey ? cursorConversation?.state?.waiting : undefined}
                  inputRef={(element) => {
                    if (element === null) {
                      composerInputs.current.delete(row.key);
                    } else {
                      composerInputs.current.set(row.key, element);
                    }
                  }}
                />
              </div>
            ))}
            {composerRows(visitedSteps, cursorStep).map((step) => {
              const calls = stepCalls(clients, step.id);
              return (
                <div key={step.id} class="reply" hidden={step.key !== cursorStepKey}>
                  <StepBox
                    label={step.label}
                    nudge={calls.nudge}
                    hold={calls.hold}
                    stop={calls.stop}
                    inputRef={(element) => {
                      if (element === null) {
                        stepInputs.current.delete(step.key);
                      } else {
                        stepInputs.current.set(step.key, element);
                      }
                    }}
                  />
                </div>
              );
            })}
          </div>
        </section>
        <section class="read pinned" aria-label="pinned reader" hidden={pinned === null}>
          {pinned !== null && (
            <ReaderHead label={pinned.label} where={rowWhere(pinned)}>
              <Pill model={rowPill(pinned.state, pinnedConversation?.state?.pane)} now={now} />
            </ReaderHead>
          )}
          {pinned !== null && (
            <Reader
              conversation={pinnedConversation}
              seenAt={pinned.seen}
              open={false}
            />
          )}
          <div class="send">
            <WorkNote pane={pinnedConversation?.state?.pane} now={now} />
            {composerRows(pinnedRows, pinned).map((row) => (
              <div key={row.key} class="pin-reply" hidden={row.key !== pinnedKey}>
                <Composer
                  label={row.label}
                  send={(text, promptId) => actions.send(clients, row, text, promptId)}
                  pull={row.kind === "pane" ? () => actions.pullQueued(clients, row) : null}
                  newPromptId={actions.newPromptId}
                  queued={row.key === pinnedKey ? pinnedConversation?.state?.waiting : undefined}
                  inputRef={(element) => {
                    if (element === null) {
                      pinnedInputs.current.delete(row.key);
                    } else {
                      pinnedInputs.current.set(row.key, element);
                    }
                  }}
                />
              </div>
            ))}
          </div>
        </section>
        <aside class="side" aria-label="epic" hidden={cursorEpic === null}>
          {cursorEpic !== null &&
            (epicDetail === undefined ? (
              <p class="side-note">{epicUnavailableText}</p>
            ) : (
              <EpicView detail={epicDetail} cursorStep={cursorStepId} />
            ))}
        </aside>
      </div>
      {helpOpen && (
        <section class="keys" aria-label="keys">
          <dl>
            {helpEntries.map(([keys, text]) => (
              <div key={text} class="key">
                <dt>
                  {keys.map((key) => (
                    <kbd key={key}>{key}</kbd>
                  ))}
                </dt>
                <dd>{text}</dd>
              </div>
            ))}
          </dl>
        </section>
      )}
    </main>
  );
}

// readerQuestion is the question the cursor's reader draws: a waiting pane row's stored question, or a session's open
// request as its stream holds it. A row with no question draws none.
function readerQuestion(row: Row, conversation: Conversation | null): ParsedQuestion | null {
  if (row.kind === "pane") {
    return row.state === stateWaiting && row.question !== "" ? parseQuestion(row.question) : null;
  }
  const request = conversation?.state?.request;
  if (request === undefined) {
    return null;
  }
  return { title: request.question, options: request.options.map((label) => ({ label, description: "" })) };
}

// RowOptions draws a waiting row's line under its text: the answer a landed answer left, or else the options its keys
// answer as chips. Chips are labels, not buttons. A row that is not waiting draws neither.
function RowOptions({ row, conversation, answer }: { row: Row; conversation: Conversation | null; answer?: Answer }) {
  if (row.state !== stateWaiting) {
    return null;
  }
  if (answer !== undefined && answer.question === row.question) {
    return <div class="ans">{`You answered ${answer.choice} · ${answer.label}`}</div>;
  }
  const options = readerQuestion(row, conversation)?.options ?? [];
  if (options.length === 0) {
    return null;
  }
  return (
    <div class="opts">
      {options.map((option, index) => {
        const keyed = index < keyedOptions;
        return (
          <span class="opt" key={index}>
            {keyed && <kbd>{index + 1}</kbd>}
            {keyed ? optionText(option.label, index + 1) : option.label}
          </span>
        );
      })}
    </div>
  );
}

// keepsAnswer reports whether a landed answer stays on its row: the row is missing from the list, or it still waits on
// the question the answer named.
function keepsAnswer(rows: Row[], key: string, answer: Answer): boolean {
  const row = rows.find((candidate) => candidate.key === key);
  return row === undefined || (row.state === stateWaiting && row.question === answer.question);
}

// optionText is an option's label without the number of its own key cap, when the label starts with that number. A
// label without it is named as it is.
function optionText(label: string, choice: number): string {
  const numbered = `${choice} `;
  return label.startsWith(numbered) ? label.slice(numbered.length) : label;
}

// matchesFilter keeps a row whose label, project, host, state or summary holds every typed word.
function matchesFilter(row: Row, words: string[]): boolean {
  const haystack = [row.label, row.project, row.host, row.state, row.summary].join(" ").toLowerCase();
  return words.every((word) => haystack.includes(word));
}

// readAsSeen clears a row's unseen mark once its seen write succeeded, until its changed time moves past the one it had.
function readAsSeen(row: Row, written: Map<string, number>): Row {
  const writtenAt = written.get(row.key);
  return row.unseen && writtenAt !== undefined && row.changed <= writtenAt ? { ...row, unseen: false } : row;
}

// keepEntries adds the entry to the entries that keep a box and refreshes each kept entry from the latest list. An
// entry missing from the list is kept as it was. It returns previous itself when nothing changes, so an unchanged list
// does not render again.
function keepEntries<T extends { key: string }>(previous: T[], latest: T[], added: T | null): T[] {
  const kept = previous.map((held) => latest.find((entry) => entry.key === held.key) ?? held);
  if (added !== null && !kept.some((entry) => entry.key === added.key)) {
    kept.push(added);
  }
  const unchanged = kept.length === previous.length && kept.every((entry, position) => entry === previous[position]);
  return unchanged ? previous : kept;
}

// composerRows returns the entries that render a box: the kept entries, and the shown entry before the effect has added
// it.
function composerRows<T extends { key: string }>(kept: T[], shown: T | null): T[] {
  if (shown === null || kept.some((entry) => entry.key === shown.key)) {
    return kept;
  }
  return [...kept, shown];
}

// panesClass names the grid the columns sit in: split when a row is pinned, and with-step when a step's reader and its
// epic column both show.
function panesClass(split: boolean, withStep: boolean): string {
  return `panes${split ? " split" : ""}${withStep ? " with-step" : ""}`;
}

// stepCalls are the owner's calls on one step, each naming the step by its id: Steps.Nudge with the text, Steps.Hold,
// and Steps.Stop. The page has no other call on a step.
function stepCalls(clients: Clients, id: string) {
  return {
    nudge: async (text: string): Promise<void> => {
      await clients.steps.nudge({ id, text });
    },
    hold: async (): Promise<void> => {
      await clients.steps.hold({ id });
    },
    stop: async (): Promise<void> => {
      await clients.steps.stop({ id });
    },
  };
}

// resolveIndex finds the cursor's entry, or the index it sat at, clamped into the entries; -1 when there are none.
function resolveIndex(items: Item[], cursor: Cursor): number {
  if (items.length === 0) {
    return -1;
  }
  const found = items.findIndex((item) => item.key === cursor.key);
  return found >= 0 ? found : clamp(cursor.index, items.length);
}

// isRow tells a row from an epic or step entry by its kind.
function isRow(item: Item): item is Row {
  return item.kind === "pane" || item.kind === "session";
}

// epicIdOf is the id of the epic an epic or step entry belongs to, which the epics' details are held by.
function epicIdOf(entry: EpicItem | StepItem): string {
  return entry.kind === "epic" ? entry.id : entry.epic;
}

// entriesOf lists a section's entries in the order they are drawn: each epic with its steps, then the rows.
function entriesOf(section: EpicSection): Item[] {
  return [...section.epics.flatMap((block) => [block.epic, ...block.steps]), ...section.rows];
}

// EpicEntry draws an epic's line: its gauge, then its slug, repository and readout. A full epic also draws its strip of
// one segment per record task.
function EpicEntry({
  epic,
  full,
  current,
  onSelect,
}: {
  epic: EpicItem;
  full: boolean;
  current: boolean;
  onSelect: (item: Item) => void;
}) {
  return (
    <div
      class={current ? "b-row epic cur" : "b-row epic"}
      data-row=""
      data-key={epic.key}
      aria-current={current ? "true" : undefined}
      onClick={(event) => {
        if (!isLinkClick(event)) {
          onSelect(epic);
        }
      }}
    >
      {epic.known && <Gauge done={epic.done} total={epic.total} />}
      <div class="main">
        <div class="l1">
          <span class="label">{epic.slug}</span>
          <span class="proj">{epic.repository}</span>
          <span class="rt">{epicReadoutText(epic)}</span>
        </div>
        {full && epic.strip.length > 0 && (
          <div class={epic.strip.length > denseStrip ? "strip dense" : "strip"}>
            {epic.strip.map((tone) => (
              <i class={`k-${tone}`} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

// epicReadoutText is an epic's outcome and count, joined, with an empty outcome left out. Without detail the count
// reads tasks unavailable.
function epicReadoutText(epic: EpicItem): string {
  const count = epic.known ? `${epic.done} of ${epic.total}` : tasksUnavailableText;
  return [epic.outcome, count].filter((part) => part !== "").join(" · ");
}

// StepEntry draws a step's line: its mark, label and five pips, then its held text when it is held, or its doing text
// as text.
function StepEntry({
  step,
  current,
  onSelect,
}: {
  step: StepItem;
  current: boolean;
  onSelect: (item: Item) => void;
}) {
  return (
    <div
      class={`b-row step${step.held ? " held" : ""}${current ? " cur" : ""}`}
      data-row=""
      data-key={step.key}
      aria-current={current ? "true" : undefined}
      onClick={(event) => {
        if (!isLinkClick(event)) {
          onSelect(step);
        }
      }}
    >
      {step.held ? (
        <span class="um" aria-hidden="true">
          {heldMark}
        </span>
      ) : (
        <span class="g g-working" aria-hidden="true" />
      )}
      <span class="label">{step.label}</span>
      <span class="pips">
        {step.pips.map((kind) => (
          <i class={`k-${kind}`} />
        ))}
      </span>
      {step.held ? (
        <span class="held">{step.reason === "" ? "held" : `held · ${step.reason}`}</span>
      ) : (
        <ReplyText text={step.doing} />
      )}
    </div>
  );
}

// Gauge draws the done share of the tasks as an arc over a full circle. A circle of no tasks gets no arc.
function Gauge({ done, total }: { done: number; total: number }) {
  const share = total === 0 ? 0 : done / total;
  return (
    <svg class="gauge" aria-hidden="true" viewBox="0 0 36 36">
      <circle class="gauge-rest" cx="18" cy="18" r="16" pathLength="1" />
      <circle class="gauge-done" cx="18" cy="18" r="16" pathLength="1" stroke-dasharray={`${share} 1`} />
    </svg>
  );
}

// isLinkClick reports whether a click or double click landed on a link, which opens it and moves nothing else.
function isLinkClick(event: Event): boolean {
  return event.target instanceof Element && event.target.closest("a") !== null;
}

function clamp(index: number, length: number): number {
  return Math.min(Math.max(index, 0), length - 1);
}

function sectionTitle(section: EpicSection): string {
  if (section.title !== "") {
    return section.title;
  }
  return section.rows[0]?.host || noDirectoryTitle;
}

// sectionClass marks the two heads that ask for a look: Needs you, and New since you looked.
function sectionClass(title: string): string {
  if (title === needsYouTitle) {
    return "b-sec need";
  }
  if (title === newSinceTitle) {
    return "b-sec fresh";
  }
  return "b-sec";
}

// rowWhere is a row's where line: its project's last path element and its host, joined, with an empty part left out.
function rowWhere(row: Row): string {
  return [lastPathElement(row.project), row.host].filter((part) => part !== "").join(" · ");
}

// stepWhere is a step's where line: its epic, its agent, and its round.
function stepWhere(step: StepItem): string {
  return `${step.epicSlug} › ${step.agent} · step · round ${step.round}`;
}

function lastPathElement(path: string): string {
  const parts = path.split("/").filter((part) => part !== "");
  return parts[parts.length - 1] ?? "";
}

function ageText(changed: number, now: number): string {
  if (changed <= 0) {
    return "";
  }
  const seconds = Math.max(0, now - changed);
  if (seconds < secondsPerMinute) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / secondsPerMinute);
  if (minutes < minutesPerHour) {
    return `${minutes}m`;
  }
  const hours = Math.floor(minutes / minutesPerHour);
  if (hours < hoursPerDay) {
    return `${hours}h`;
  }
  return `${Math.floor(hours / hoursPerDay)}d`;
}

import { Fragment } from "preact";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";

import * as actions from "./actions";
import type { Clients } from "./actions";
import { Composer } from "./composer";
import { ContractBody } from "./contract";
import type { Conversation } from "./conversation";
import { type EpicCommands, EpicPage } from "./epicpage";
import {
  denseStrip,
  type EpicItem,
  type EpicSection,
  type EpicsView,
  noEpics,
  placeEpics,
  type StepItem,
  withEpicsGroup,
} from "./epics";
import { type Followed, follow } from "./follow";
import type { EpicHistoryEntry, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import type { InAppNotice, NoticeKey } from "./gen/orrery/v1/notice_pb";
import type { HostStatusResponse, Pane } from "./gen/orrery/v1/orrery_pb";
import { Mark } from "./mark";
import { MetersButton, MetersPanel } from "./meters";
import { NoticeBanner } from "./notices";
import { PhaseOutputPage } from "./phaseoutput";
import type { Phase } from "./phases";
import { type ParsedQuestion, parseQuestion } from "./question";
import { Reader, ReaderHead } from "./reader";
import { byAttention, byProject, type Row } from "./rows";
import { type SeenClock, type SeenWindow, watchSeen } from "./seen";
import { StepBox } from "./stepbox";
import { ReplyText } from "./text";
import { TitleRow } from "./titlerow";
import { WorkerSessionPage } from "./workersession";
import { Pill, rowPill, stepPill } from "./workline";

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

// The page the epic page's section holds: the epic page, the contract its summary opens, a task's phase output, or a
// step's worker session.
type PageView =
  | { kind: "epic" }
  | { kind: "contract" }
  | { kind: "phase"; task: string; phase: Phase }
  | { kind: "session"; task: string; phase: Phase; step: string };

// A page under the open one, with the scroll place it had when the open page opened over it.
interface PageBelow {
  page: PageView;
  scroll: number;
}

// The epic page's view: the page on top, and the pages under it, bottom first. The epic page is always the first of
// them when a deeper page is open. move is the class the page section carries while it moves: drill as a page opens
// deeper, leave as it comes back. closed is set by back and the orrery crumb, and a move of the cursor opens the page
// again.
interface EpicPageState {
  page: PageView;
  below: PageBelow[];
  move: "" | "drill" | "leave";
  closed: boolean;
}

const epicPageView: PageView = { kind: "epic" };
const openEpicPage: EpicPageState = { page: epicPageView, below: [], move: "", closed: false };
const closedEpicPage: EpicPageState = { page: epicPageView, below: [], move: "", closed: true };

// An epic's history as it was read, with the id of the epic it belongs to, so a page never shows another epic's
// entries.
interface EpicHistory {
  id: string;
  entries: EpicHistoryEntry[];
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
const commandFailedText = "epic command failed";
const sessionUnavailableText = "session unavailable";
// The most entries one page of an epic's history asks for.
const historyPageSize = 200;
const heldMark = "‖";

// The keys 1-3 answer the first three options of a question, and no others.
const keyedOptions = 3;

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
  [["m"], "open or close the host's meters"],
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
  notices = [],
  host = null,
}: {
  rows: Row[];
  error: string;
  now: number;
  clients: Clients;
  seenClock?: SeenClock;
  seenWindow?: SeenWindow;
  epics?: EpicsView;
  notices?: InAppNotice[];
  host?: HostStatusResponse | null;
}) {
  const [view, setView] = useState<View>("attention");
  const [cursor, setCursor] = useState<Cursor>({ key: "", index: 0 });
  const [filter, setFilter] = useState("");
  const [helpOpen, setHelpOpen] = useState(false);
  const [open, setOpen] = useState(false);
  const [metersOpen, setMetersOpen] = useState(false);
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
  // Whether the accepted, waiting epics are open under their line. They start closed.
  const [waitingOpen, setWaitingOpen] = useState(false);
  // The fixed line an epic page's command leaves when it fails. A command that lands clears it.
  const [commandError, setCommandError] = useState("");
  const [epicHistory, setEpicHistory] = useState<EpicHistory>({ id: "", entries: [] });
  const [epicPage, setEpicPage] = useState<EpicPageState>(openEpicPage);
  // The worker session the page shows, held under the step it belongs to.
  const [sessionStream, setSessionStream] = useState<Stream | null>(null);
  // The epic page section, and the scroll place a back restores once the page it returns to is drawn.
  const pageRef = useRef<HTMLElement>(null);
  const restorePageScroll = useRef<number | null>(null);
  // loadArtifact reads a run's log or artifact for the epic page. It is stable across renders, so an open log or image
  // is not loaded again on every poll.
  const loadArtifact = useCallback(
    (run: string, name: string, stderr: boolean) => clients.evidence.artifact({ run, name, stderr }),
    [clients],
  );
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
  const flat = sections.flatMap((section) => entriesOf(section, waitingOpen));
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
  // The cursor's epic entry, which the epic page draws beside the list. A step entry is no epic entry: its reader shows
  // alone.
  const epicEntry = cursorItem?.kind === "epic" ? cursorItem : null;
  const stepEntries = flat.filter((item): item is StepItem => item.kind === "step");
  const epicDetail = epicEntry === null ? undefined : epics.details.get(epicEntry.id);
  // The history is read for the epic entry the cursor rests on, once its detail is held. It is read again when the
  // epic's last write moves.
  const historyId = epicEntry !== null && epicDetail !== undefined ? epicEntry.id : "";
  const epicChanged = epicDetail?.epic?.changed ?? 0n;
  const shownIndex = Math.max(index, 0);
  // The step whose worker session the open page shows. Its session is followed only while the page is open on a step
  // the epic's detail holds, so a step the detail lacks opens no stream.
  const openStep = !epicPage.closed && epicPage.page.kind === "session" ? epicPage.page.step : "";
  const shownSession = epicDetail?.steps.some((step) => step.id === openStep) === true ? openStep : "";

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

  // The worker session the page shows is followed while the page shows it. A back or close past it, or a move of the
  // cursor, which opens the epic page afresh, stops its stream.
  useEffect(() => {
    if (shownSession === "") {
      return undefined;
    }
    return follow(clients, { kind: "session", id: shownSession }, (conversation) =>
      setSessionStream({ key: shownSession, conversation }),
    );
  }, [shownSession, clients]);

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

  // The history is read page by page for the epic entry the cursor rests on. A read that ends after the cursor has
  // moved on is dropped.
  useEffect(() => {
    if (historyId === "") {
      return undefined;
    }
    let current = true;
    void readHistory(clients, historyId).then(
      (entries) => {
        if (current) {
          setEpicHistory({ id: historyId, entries });
        }
      },
      () => {
        if (current) {
          setEpicHistory({ id: historyId, entries: [] });
        }
      },
    );
    return () => {
      current = false;
    };
  }, [historyId, epicChanged, clients]);

  // A move of the cursor opens the epic page afresh, so a page a back closed or a contract opened does not outlast it.
  useLayoutEffect(() => {
    setEpicPage(openEpicPage);
  }, [cursorKey]);

  // A back restores the scroll place of the page it returns to before that page is painted.
  useLayoutEffect(() => {
    if (restorePageScroll.current !== null && pageRef.current !== null) {
      pageRef.current.scrollTop = restorePageScroll.current;
    }
    restorePageScroll.current = null;
  }, [epicPage]);

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
  const toggleMeters = () => setMetersOpen((value) => !value);
  // drill opens a page over the open one. The open page's scroll place is kept for the back that returns to it, and the
  // new page opens at its top.
  const drill = (next: PageView) => {
    const page = pageRef.current;
    const scroll = page?.scrollTop ?? 0;
    if (page !== null) {
      page.scrollTop = 0;
    }
    setEpicPage((previous) => ({
      page: next,
      below: [...previous.below, { page: previous.page, scroll }],
      move: "drill",
      closed: false,
    }));
  };
  // leaveTo returns to the page under the open one at index, whose scroll place the layout effect puts back. The pages
  // above it close.
  const leaveTo = (index: number) => {
    const target = epicPage.below[index];
    restorePageScroll.current = target.scroll;
    setEpicPage({ page: target.page, below: epicPage.below.slice(0, index), move: "leave", closed: false });
  };
  // back returns from the page on top to the page under it. toEpicPage returns to the epic page from any page over it.
  const back = () => leaveTo(epicPage.below.length - 1);
  const toEpicPage = () => leaveTo(0);
  // openPhase opens a task's phase output over the epic page. On the phase output, a head opens its phase in place.
  const openPhase = (task: string, phase: Phase) => {
    if (epicPage.page.kind === "phase") {
      setEpicPage((previous) => ({ ...previous, page: { kind: "phase", task, phase } }));
      return;
    }
    drill({ kind: "phase", task, phase });
  };
  const openContract = () => drill({ kind: "contract" });
  const closeEpicPage = () => setEpicPage(closedEpicPage);

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

  // openNotice counts a notice seen and moves the cursor to the entry its key names, when the list shows it. A failed
  // report is dropped; the notice stays in the banner until it ends.
  const openNotice = (notice: InAppNotice) => {
    void clients.notices.seen({ key: notice.key }).catch(() => undefined);
    const entry = noticeEntry(flat, notice.key);
    if (entry !== undefined) {
      selectItem(entry);
    }
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
    // Keys typed in a text box are text: only Escape leaves it. That covers the reply boxes and the answer box.
    const focused = document.activeElement;
    if (focused instanceof HTMLElement && (focused.tagName === "TEXTAREA" || focused.tagName === "INPUT")) {
      if (event.key === "Escape") {
        focused.blur();
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
      case "m":
        if (host !== null) {
          toggleMeters();
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

  // pageNode draws the page the section holds over the epic's detail: the epic page, or the contract, the phase output
  // or the worker session the page has moved to.
  const pageNode = (entry: EpicItem, detail: EpicsGetResponse) => {
    const { page } = epicPage;
    if (page.kind === "contract" && detail.contract !== undefined) {
      return (
        <>
          <TitleRow
            crumbs={[
              { label: "orrery", onGo: closeEpicPage },
              { label: detail.epic?.slug ?? "", onGo: toEpicPage },
              { label: "contract" },
            ]}
            onBack={back}
          />
          <ContractBody contract={detail.contract} requirements={detail.requirements} />
        </>
      );
    }
    if (page.kind === "phase") {
      return (
        <PhaseOutputPage
          key={page.task}
          detail={detail}
          epic={detail.epic?.slug ?? ""}
          task={page.task}
          phase={page.phase}
          now={now}
          loadArtifact={loadArtifact}
          onBack={back}
          onGoList={closeEpicPage}
          onOpenPhase={openPhase}
          onOpenSession={(step) => drill({ kind: "session", task: page.task, phase: page.phase, step })}
          hold={(step) => stepCalls(clients, step).hold()}
          stop={(step) => stepCalls(clients, step).stop()}
        />
      );
    }
    if (page.kind === "session") {
      const step = detail.steps.find((candidate) => candidate.id === page.step);
      if (step === undefined) {
        return <p class="side-note">{sessionUnavailableText}</p>;
      }
      const calls = stepCalls(clients, step.id);
      return (
        <WorkerSessionPage
          conversation={sessionStream !== null && sessionStream.key === page.step ? sessionStream.conversation : null}
          step={step}
          epic={detail.epic?.slug ?? ""}
          task={page.task}
          phase={page.phase}
          onGoList={closeEpicPage}
          onGoEpic={toEpicPage}
          onBack={back}
          nudge={calls.nudge}
          hold={calls.hold}
          stop={calls.stop}
        />
      );
    }
    return (
      <EpicPage
        detail={detail}
        history={epicHistory.id === entry.id ? epicHistory.entries : []}
        now={now}
        loadArtifact={loadArtifact}
        commands={epicCommands(clients, entry.id, setCommandError)}
        onOpenContract={openContract}
        onOpenPhase={openPhase}
        onBack={closeEpicPage}
      />
    );
  };

  return (
    <main class="app">
      {notices.length > 0 && <NoticeBanner notices={notices} onOpen={openNotice} />}
      <div class={pinned !== null ? "panes split" : "panes"}>
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
            {commandError !== "" && (
              <p class="error" role="alert">
                {commandError}
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
                {section.waiting.length > 0 && (
                  <div class="b-more" onClick={() => setWaitingOpen((value) => !value)}>
                    {`${section.waiting.length} accepted, waiting to start`}
                  </div>
                )}
                {waitingOpen &&
                  section.waiting.map((epic) => (
                    <EpicEntry
                      key={epic.key}
                      epic={epic}
                      full={false}
                      current={epic.key === cursorKey}
                      onSelect={selectItem}
                    />
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
          {host !== null && metersOpen && <MetersPanel stats={host.stats} />}
          <footer class="lfoot">
            {hosts.map((name) => (
              <span key={name} class="host">
                {name}
              </span>
            ))}
            {host !== null && <MetersButton status={host} onToggle={toggleMeters} />}
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
            {composerRows(visited, cursorRow).map((row) => (
              <div key={row.key} class="reply" hidden={row.key !== cursorRowKey}>
                <Composer
                  label={row.label}
                  state={row.key === cursorRowKey ? headState(row, cursorConversation?.state?.pane) : row.state}
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
                    held={step.held}
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
            {composerRows(pinnedRows, pinned).map((row) => (
              <div key={row.key} class="pin-reply" hidden={row.key !== pinnedKey}>
                <Composer
                  label={row.label}
                  state={row.key === pinnedKey ? headState(row, pinnedConversation?.state?.pane) : row.state}
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
        <section
          class={epicPage.move === "" ? "page" : `page ${epicPage.move}`}
          aria-label="epic"
          hidden={epicEntry === null || epicPage.closed}
          ref={pageRef}
        >
          {epicEntry !== null &&
            (epicDetail === undefined ? (
              <p class="side-note">{epicUnavailableText}</p>
            ) : (
              pageNode(epicEntry, epicDetail)
            ))}
        </section>
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

// headState is the state a column's head pill reads: a pane given speaks for the row, and a row with no pane reads its
// own state.
function headState(row: Row, pane: Pane | undefined): string {
  return pane?.state ?? row.state;
}

// composerRows returns the entries that render a box: the kept entries, and the shown entry before the effect has added
// it.
function composerRows<T extends { key: string }>(kept: T[], shown: T | null): T[] {
  if (shown === null || kept.some((entry) => entry.key === shown.key)) {
    return kept;
  }
  return [...kept, shown];
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

// noticeEntry finds the entry a notice's key names among the entries the list shows: a pane row by pane id, a session
// row or a step by session id, or an epic by epic id. A key that names none, such as a limit's, finds nothing.
function noticeEntry(items: Item[], key: NoticeKey | undefined): Item | undefined {
  const pane = key?.pane ?? "";
  const session = key?.session ?? "";
  const epic = key?.epic ?? "";
  return items.find((item) => {
    if (item.kind === "pane") {
      return pane !== "" && item.id === pane;
    }
    if (item.kind === "epic") {
      return epic !== "" && item.id === epic;
    }
    return session !== "" && item.id === session;
  });
}

// readHistory reads an epic's history from its first entry, page by page, until a page names no next cursor. A failed
// page fails the whole read.
async function readHistory(clients: Clients, id: string): Promise<EpicHistoryEntry[]> {
  const entries: EpicHistoryEntry[] = [];
  let after = "";
  do {
    const page = await clients.epics.history({ id, after, limit: historyPageSize });
    entries.push(...page.entries);
    after = page.next;
  } while (after !== "");
  return entries;
}

// epicCommands binds an epic's commands to its id. A command that fails reports commandFailedText through report, and
// one that lands reports "". answer resolves "" when the answer lands, else the error's message.
function epicCommands(clients: Clients, epicId: string, report: (line: string) => void): EpicCommands {
  const send = (sent: Promise<unknown>): void => {
    void sent.then(
      () => report(""),
      () => report(commandFailedText),
    );
  };
  return {
    pause: () => send(clients.epics.pause({ id: epicId })),
    resume: () => send(clients.epics.resume({ id: epicId })),
    stop: () => send(clients.epics.stop({ id: epicId })),
    retry: () => send(clients.epics.retry({ id: epicId })),
    answer: (question, text) =>
      clients.epics.answer({ epicId, id: crypto.randomUUID(), text, question }).then(
        () => "",
        (error: unknown) => (error instanceof Error && error.message !== "" ? error.message : answerFailedText),
      ),
  };
}

// entriesOf lists a section's entries in the order they are drawn: each epic with its steps, then the waiting epics
// while their line is open, then the rows.
function entriesOf(section: EpicSection, waitingOpen: boolean): Item[] {
  return [
    ...section.epics.flatMap((block) => [block.epic, ...block.steps]),
    ...(waitingOpen ? section.waiting : []),
    ...section.rows,
  ];
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

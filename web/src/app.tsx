import { Fragment } from "preact";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";

import * as actions from "./actions";
import type { Clients } from "./actions";
import { type ComposerHandle, Composer } from "./composer";
import { ContractBody } from "./contract";
import type { Conversation } from "./conversation";
import { ArchiveButton, type ArchiveControl, EpicLine } from "./epicline";
import { type EpicCommands, EpicPage, type Landing } from "./epicpage";
import {
  type EpicBlock,
  type EpicItem,
  type EpicSection,
  type EpicsView,
  endedWords,
  epicsView,
  keepUnderCursor,
  noEpics,
  placeEpics,
  type StepItem,
  withEpicsGroup,
} from "./epics";
import { type Followed, follow } from "./follow";
import type { EpicHistoryEntry, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import type { InAppNotice, NoticeKey } from "./gen/orrery/v1/notice_pb";
import type { HostStatusResponse, Pane } from "./gen/orrery/v1/orrery_pb";
import { Handover } from "./handover";
import { Mark } from "./mark";
import { MetersButton, MetersPanel } from "./meters";
import { NeedEntry } from "./needentry";
import { epicNeeds, rowNeed } from "./needs";
import { NoticeBanner } from "./notices";
import { PhaseOutputPage } from "./phaseoutput";
import type { Phase } from "./phases";
import { PickerPalette } from "./picker";
import { type Viewing, viewingOf } from "./presence";
import { type ParsedQuestion, parseQuestion } from "./question";
import { ConversationHead, Reader, ReaderHead } from "./reader";
import { byAttention, byProject, type Row, type Section, splitArchived } from "./rows";
import { type SeenClock, type SeenWindow, watchSeen } from "./seen";
import { type SendKind, type Sending, type SentText, sendingEntry, unsentEntries, withoutTexts } from "./sending";
import { StepBox } from "./stepbox";
import { Talk, type TalkTarget } from "./talk";
import { TalkDock } from "./talkdock";
import { ReplyText } from "./text";
import { TitleRow } from "./titlerow";
import { WorkerSessionPage } from "./workersession";
import { Pill, rowPill, stepPill } from "./workline";

type View = "attention" | "project" | "archived";

// The conversation the reader holds, with the row key it belongs to, so a row never shows another row's transcript.
interface Stream {
  key: string;
  conversation: Conversation;
}

// The row an Escape acts on: the row, the column whose composer holds the key, and the row's state at the press.
interface EscapeTarget {
  row: Row;
  column: "cursor" | "pinned";
  state: string;
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
// again. landing is the block an opened notice landed the epic page on; a page a drill, a back or a move opens holds
// none.
interface EpicPageState {
  page: PageView;
  below: PageBelow[];
  move: "" | "drill" | "leave";
  closed: boolean;
  landing?: Landing;
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

// The conversation Talk docked beside the epic page, and the epic it opened on: showing another epic closes it.
interface TalkDockState {
  epic: string;
  target: TalkTarget;
}

// An entry the list shows and the cursor can rest on: a row, or an epic or step of the Epics group.
type Item = Row | EpicItem | StepItem;

// The row the palette answered: the pane Picker.Run answered, or the session Control.Start started, by its kind and id.
interface PendingTarget {
  kind: Row["kind"];
  id: string;
}

const stateWaiting = "waiting";
const stateWorking = "working";
// The block kind a notice names when it opens the contract page.
const contractBlock = "contract";
const needsYouTitle = "Needs you";
const newSinceTitle = "New since you looked";
const noDirectoryTitle = "no directory";
const epicUnavailableText = "epic unavailable";
const seenFailedText = "could not mark seen";
const answerFailedText = "no answer was sent";
const takeBackFailedText = "could not take the queued messages back";
const interruptFailedText = "could not interrupt";
const commandFailedText = "epic command failed";
const sessionUnavailableText = "session unavailable";
const archiveFailedText = "could not change archive";
const archivedTitle = "Archived";
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
  [["v"], "archived rows and epics"],
  [["A"], "archive the cursor's row or epic"],
  [["w"], "first waiting row"],
  [["n"], "next unseen row, wrapping"],
  [["N"], "open the palette to ask for a pane or a directory, outside a text box"],
  [["⌘K"], "open the palette anywhere, a text box included; Ctrl+K does too"],
  [["x", "Enter"], "mark the cursor's row seen"],
  [["e"], "mark it seen, then go to the next unseen row"],
  [["/"], "filter: Enter leaves it, Esc clears it"],
  [["i"], "focus the reply box"],
  [["1", "2", "3"], "answer the cursor's waiting question with that choice, or pick that option on an epic page"],
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
  onViewing,
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
  onViewing?: (viewing: Viewing) => void;
}) {
  const [view, setView] = useState<View>("attention");
  const [cursor, setCursor] = useState<Cursor>({ key: "", index: 0 });
  const [filter, setFilter] = useState("");
  const [helpOpen, setHelpOpen] = useState(false);
  const [open, setOpen] = useState(false);
  const [metersOpen, setMetersOpen] = useState(false);
  // Whether the picker palette is open.
  const [pickerOpen, setPickerOpen] = useState(false);
  // The row the palette answered, which the cursor moves to once the loaded rows hold it: the pane Picker.Run
  // answered, or the session Control.Start started.
  const [pending, setPending] = useState<PendingTarget | null>(null);
  const [stream, setStream] = useState<Stream | null>(null);
  // The rows the cursor has rested on, each with its own composer, so a draft waits where it was typed. A row stays
  // here while it is missing from the list, as it is for a poll whose list call failed, so its draft is not lost.
  const [visited, setVisited] = useState<Row[]>([]);
  // The steps the cursor has rested on, each with its own step box, kept as the visited rows are so a draft survives.
  const [visitedSteps, setVisitedSteps] = useState<StepItem[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);
  const composerInputs = useRef(new Map<string, HTMLTextAreaElement>());
  const stepInputs = useRef(new Map<string, HTMLTextAreaElement>());
  // The handles of the cursor's and the pinned row's composers, by row key, which an Escape calls.
  const composerHandles = useRef(new Map<string, ComposerHandle>());
  const pinnedHandles = useRef(new Map<string, ComposerHandle>());
  // The row | pinned beside the list, with its own stream. The rows that have been pinned each keep a composer, as the
  // visited rows do, so a draft or a failed send survives the pin's close and its reopening on that row.
  const [pinned, setPinned] = useState<Row | null>(null);
  const [pinStream, setPinStream] = useState<Stream | null>(null);
  const [pinnedRows, setPinnedRows] = useState<Row[]>([]);
  const pinnedInputs = useRef(new Map<string, HTMLTextAreaElement>());
  // The older page of each followed stream. Its reader calls it at the reader's top, to load the page before its oldest
  // item; each follow effect sets its own.
  const cursorOlder = useRef<() => void>(() => {});
  const pinnedOlder = useRef<() => void>(() => {});
  const sessionOlder = useRef<() => void>(() => {});
  const pendingG = useRef(false);
  const handler = useRef<(event: KeyboardEvent) => void>();
  // The changed time each row had when its seen write succeeded. A row reads as seen until its changed time moves past it.
  const [seenWritten, setSeenWritten] = useState<Map<string, number>>(() => new Map());
  // The endings a seen write landed for, by endingKey. An ended epic reads as seen until a later ending.
  const [seenEpics, setSeenEpics] = useState<Set<string>>(() => new Set());
  const [seenError, setSeenError] = useState("");
  const [answerError, setAnswerError] = useState("");
  // The fixed line an Escape leaves when taking queued messages back or interrupting a row fails.
  const [escapeError, setEscapeError] = useState("");
  const [archiveError, setArchiveError] = useState("");
  // The archive marks the landed Archive and Unarchive calls left, by row key and by epic id. Each shows in place of
  // the list's own archived field until the next list answer, which carries that field.
  const [rowMarks, setRowMarks] = useState<Map<string, boolean>>(() => new Map());
  const [epicMarks, setEpicMarks] = useState<Map<string, boolean>>(() => new Map());
  // Whether the accepted, waiting epics are open under their line. They start closed.
  const [waitingOpen, setWaitingOpen] = useState(false);
  // Whether the seen ended epics are open under their ended earlier line. They start closed.
  const [endedOpen, setEndedOpen] = useState(false);
  // The detail each epic the poll did not load answers with when its page opens. An epic is asked for once, so the
  // answers are kept here and the asked ids in a ref.
  const [askedDetails, setAskedDetails] = useState<Map<string, EpicsGetResponse>>(() => new Map());
  const askedEpics = useRef(new Set<string>());
  // The fixed line an epic page's command leaves when it fails. A command that lands clears it.
  const [commandError, setCommandError] = useState("");
  const [epicHistory, setEpicHistory] = useState<EpicHistory>({ id: "", entries: [] });
  const [epicPage, setEpicPage] = useState<EpicPageState>(openEpicPage);
  // The landing the last opened notice names. Each notice makes its own, so its effect applies it once.
  const [landing, setLanding] = useState<Landing | null>(null);
  // The worker session the page shows, held under the step it belongs to.
  const [sessionStream, setSessionStream] = useState<Stream | null>(null);
  // The conversation Talk docked beside the epic page, with the epic it opened on.
  const [talkDock, setTalkDock] = useState<TalkDockState | null>(null);
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
  // The messages each row's composer sent that the row's stored conversation does not hold yet, by row key. Each shows
  // as sending in its row's reader until its stored prompt arrives.
  const [sendingByRow, setSendingByRow] = useState<Map<string, Sending[]>>(() => new Map());
  const seenWatch = useRef<ReturnType<typeof watchSeen>>();
  // The row or unseen ended epic the seen watch last rested on, which its write marks seen.
  const restedSeen = useRef<Row | EpicItem | null>(null);

  const words = filter
    .toLowerCase()
    .split(/\s+/)
    .filter((word) => word !== "");
  const shownRows = rows.map((row) => readAsSeen(withRowMark(row, rowMarks), seenWritten));
  // The brand's counts read every loaded row as the list reads it after seen writes, before the filter.
  const waitingCount = shownRows.filter((row) => row.state === stateWaiting).length;
  const workingCount = shownRows.filter((row) => row.state === stateWorking).length;
  const newCount = shownRows.filter((row) => row.unseen).length;
  // The footer names the hosts the loaded rows come from, each once, in the order the rows first show them.
  const hosts = [...new Set(rows.map((row) => row.host))].filter((host) => host !== "");
  const matching = shownRows.filter((row) => matchesFilter(row, words));
  const { rows: defaultRows, archived: archivedRows } = splitArchived(matching);
  // The epics as the local archive marks leave them, which the views draw in place of the list's.
  const marked = withEpicMarks(epics, epicMarks);
  // The cursor's epic stays in Just ended while the cursor rests on it, even once it is seen. The cursor is held by
  // key, so its epic is the seen one the endedEarlier list holds under that key. While the ended earlier line is open
  // the seen epics are drawn in place, so none is kept and the cursor walks them in their drawn order.
  const seenView = readEpicsAsSeen(marked, seenEpics);
  const cursorEpicId = endedOpen ? "" : (seenView.endedEarlier.find((item) => item.key === cursor.key)?.id ?? "");
  const kept = keepUnderCursor(seenView, cursorEpicId);
  // The needs of the live epics, in list order: a live epic's Get answer carries its Epic, and the details hold exactly
  // the live epics. An archived epic's needs are left out. Only the attention view has the Needs you section that draws
  // them.
  const archivedIds = new Set(kept.archived.map((item) => item.id));
  const detailEpics = [...epics.details.values()].flatMap((detail) =>
    detail.epic === undefined || archivedIds.has(detail.epic.id) ? [] : [detail.epic],
  );
  const needs = view === "attention" ? epicNeeds(detailEpics, epics.details) : [];
  const sections =
    view === "attention"
      ? withEpicsGroup(withNeedsSection(byAttention(defaultRows), needs.length > 0), kept)
      : view === "project"
        ? placeEpics(byProject(defaultRows), kept.blocks)
        : [archivedSection(kept.archived, archivedRows)];
  const flat = sections.flatMap((section) => entriesOf(section, waitingOpen, endedOpen));
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
  // The cursor's epic entry while its ending reads unseen: the seen watch rests on it, and its write marks it seen.
  const restedEpic =
    epicEntry !== null && seenView.justEnded.some((item) => item.key === epicEntry.key) ? epicEntry : null;
  const stepEntries = flat.filter((item): item is StepItem => item.kind === "step");
  const epicDetail =
    epicEntry === null ? undefined : (epics.details.get(epicEntry.id) ?? askedDetails.get(epicEntry.id));
  // The history is read for the epic entry the cursor rests on, once its detail is held. It is read again when the
  // epic's last write moves.
  const historyId = epicEntry !== null && epicDetail !== undefined ? epicEntry.id : "";
  const epicChanged = epicDetail?.epic?.changed ?? 0n;
  const shownIndex = Math.max(index, 0);
  // The step whose worker session the open page shows. Its session is followed only while the page is open on a step
  // the epic's detail holds, so a step the detail lacks opens no stream.
  const openStep = !epicPage.closed && epicPage.page.kind === "session" ? epicPage.page.step : "";
  const shownSession = epicDetail?.steps.some((step) => step.id === openStep) === true ? openStep : "";
  // The dock shows only on the epic it opened on.
  const docked = talkDock !== null && epicEntry !== null && talkDock.epic === epicEntry.id ? talkDock : null;
  const epicId = epicEntry?.id ?? "";
  // The epic entry the cursor rests on, when the poll did not load its detail, is asked for once with Epics.Get. A
  // failed answer leaves the page at its unavailable line.
  const askedId = epicEntry?.id ?? "";
  const polledDetail = askedId !== "" && epics.details.has(askedId);

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
    const followed = follow(clients, target, (conversation) => setStream({ key: readerKey, conversation }));
    cursorOlder.current = followed.older;
    return followed.stop;
  }, [readerKey, clients]);

  // The worker session the page shows is followed while the page shows it. A back or close past it, or a move of the
  // cursor, which opens the epic page afresh, stops its stream.
  useEffect(() => {
    if (shownSession === "") {
      return undefined;
    }
    const followed = follow(clients, { kind: "session", id: shownSession }, (conversation) =>
      setSessionStream({ key: shownSession, conversation }),
    );
    sessionOlder.current = followed.older;
    return followed.stop;
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

  // A new list answer carries serve's archived field, so the archive marks held until now give way to it.
  useEffect(() => {
    setRowMarks((previous) => (previous.size === 0 ? previous : new Map()));
  }, [rows]);

  useEffect(() => {
    setEpicMarks((previous) => (previous.size === 0 ? previous : new Map()));
  }, [epics]);

  // The pinned row is followed while it stays pinned: its close, or an unmount, stops the stream.
  const pinnedKey = pinned?.key ?? "";
  useEffect(() => {
    if (pinned === null) {
      return undefined;
    }
    const followed = follow(clients, { kind: pinned.kind, id: pinned.id }, (conversation) =>
      setPinStream({ key: pinned.key, conversation }),
    );
    pinnedOlder.current = followed.older;
    return followed.stop;
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

  // An epic the poll did not load is asked for once, when the cursor rests on it, and its answer draws the page.
  useEffect(() => {
    if (askedId === "" || polledDetail || askedEpics.current.has(askedId)) {
      return;
    }
    askedEpics.current.add(askedId);
    void clients.epics.get({ id: askedId }).then(
      (answer) => setAskedDetails((previous) => new Map(previous).set(askedId, answer)),
      () => undefined,
    );
  }, [askedId, polledDetail, clients]);

  // A move of the cursor opens the epic page afresh, so a page a back closed or a contract opened does not outlast it.
  useLayoutEffect(() => {
    setEpicPage(openEpicPage);
  }, [cursorKey]);

  // onViewing reports the viewing key of the cursor's entry each time that entry's kind or id changes, so the page's
  // presence reports name what it shows.
  useEffect(() => {
    onViewing?.(viewingOf(cursorItem));
  }, [cursorItem?.kind, cursorItem?.id]);

  // Showing another epic closes the dock. A row or step shows no epic, so the dock is kept across it.
  useLayoutEffect(() => {
    if (epicId !== "") {
      setTalkDock((previous) => (previous?.epic === epicId ? previous : null));
    }
  }, [epicId]);

  // A back restores the scroll place of the page it returns to before that page is painted.
  useLayoutEffect(() => {
    if (restorePageScroll.current !== null && pageRef.current !== null) {
      pageRef.current.scrollTop = restorePageScroll.current;
    }
    restorePageScroll.current = null;
  }, [epicPage]);

  // seenWrite runs one seen write. A failure shows the fixed line and runs no landed; a landed write clears the line
  // and runs landed.
  const seenWrite = (write: () => Promise<unknown>, landed: () => void) =>
    lineWrite(write, seenFailedText, setSeenError, landed);

  // writeSeen marks a row seen through actions.markSeen. A success reads the row as seen; a failure leaves it unseen
  // and shows a fixed line.
  const writeSeen = (row: Row) =>
    seenWrite(
      () => actions.markSeen(clients, row),
      () => setSeenWritten((previous) => new Map(previous).set(row.key, row.changed)),
    );

  // writeEpicSeen marks an ended epic seen through Epics.Seen. A success reads its ending as seen; a failure leaves it
  // unseen and shows the same fixed line.
  const writeEpicSeen = (epic: EpicItem) =>
    seenWrite(
      () => clients.epics.seen({ id: epic.id }),
      () => setSeenEpics((previous) => new Set(previous).add(endingKey(epic))),
    );

  // setArchive archives a row or epic, or unarchives it in the Archived view, through Epics.Archive or Epics.Unarchive
  // with the kind and id the entry names. A pane has no archive kind, so it does nothing. A landed call marks the entry
  // as the call left it, until the next list answer; a failed call leaves the entry where it was.
  const setArchive = (entry: Row | EpicItem, archived: boolean) => {
    const kind = entry.kind === "epic" ? "epic" : entry.archiveKind;
    if (kind === "none") {
      return;
    }
    const request = { kind, id: entry.id };
    void lineWrite(
      () => (archived ? clients.epics.archive(request) : clients.epics.unarchive(request)),
      archiveFailedText,
      setArchiveError,
      () => {
        if (entry.kind === "epic") {
          setEpicMarks((previous) => new Map(previous).set(entry.id, archived));
        } else {
          setRowMarks((previous) => new Map(previous).set(entry.key, archived));
        }
      },
    );
  };

  // archiveControl is the archive button an entry's line draws: Archive in a default view, Unarchive in the Archived
  // one.
  const archiveControl = (entry: Row | EpicItem): ArchiveControl => ({
    archived: view === "archived",
    run: () => setArchive(entry, view !== "archived"),
  });

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

  // The seen watch runs for the page's life: its write marks the row or ended epic it last rested on. Unmount stops it.
  useEffect(() => {
    const watch = watchSeen(
      seenWindow ?? window,
      () => {
        const target = restedSeen.current;
        if (target === null) {
          return;
        }
        if (target.kind === "epic") {
          void writeEpicSeen(target);
        } else {
          void writeSeen(target);
        }
      },
      seenClock,
    );
    seenWatch.current = watch;
    return () => watch.stop();
  }, [clients, seenClock, seenWindow]);

  // Each render tells the watch the cursor's row, or its unseen ended epic, and whether it reads as unseen. A changed
  // row is a new key, so a change made while the cursor rests on it gets its own dwell; an ending is a key of its own.
  useEffect(() => {
    restedSeen.current = cursorRow ?? restedEpic;
    if (restedEpic !== null) {
      seenWatch.current?.rest(endingKey(restedEpic), true);
    } else {
      seenWatch.current?.rest(
        cursorRow === null ? "" : `${cursorRow.key}@${cursorRow.changed}`,
        cursorRow?.unseen ?? false,
      );
    }
  });

  // The conversation the cursor's reader holds, only when its stream belongs to the entry the cursor rests on.
  const cursorConversation = stream !== null && stream.key === readerKey ? stream.conversation : null;
  // The conversation the pinned reader holds, only when its stream belongs to the pinned row.
  const pinnedConversation =
    pinStream !== null && pinned !== null && pinStream.key === pinned.key ? pinStream.conversation : null;
  // The conversation a list row's options come from: the stream the page holds for that row, the cursor's or the pin's.
  const heldConversation = (key: string) =>
    key === cursorRowKey ? cursorConversation : key === pinnedKey ? pinnedConversation : null;
  // The messages each reader shows as sending: its row's entries the conversation it holds has not sent yet.
  const cursorSending =
    cursorRow === null ? [] : unsentEntries(sendingByRow.get(cursorRow.key) ?? [], cursorConversation, cursorRow.kind);
  const pinnedSending =
    pinned === null ? [] : unsentEntries(sendingByRow.get(pinned.key) ?? [], pinnedConversation, pinned.kind);
  // A stored prompt that a reader's stream holds ends its entry, so the entry leaves the page.
  useEffect(() => {
    setSendingByRow((previous) =>
      pruneSent(previous, [
        { key: cursorRowKey, kind: cursorRow?.kind ?? "session", conversation: cursorConversation },
        { key: pinnedKey, kind: pinned?.kind ?? "session", conversation: pinnedConversation },
      ]),
    );
  }, [cursorConversation, pinnedConversation, cursorRowKey, pinnedKey]);
  // addSending records a message a row's composer sent as it starts. held is the conversation on screen for that row,
  // which anchors a pane's match, or null when the row's stream is not held.
  const addSending = (rowKey: string, sent: SentText, held: Conversation | null) =>
    setSendingByRow((previous) =>
      new Map(previous).set(rowKey, [...(previous.get(rowKey) ?? []), sendingEntry(sent, held)]),
    );
  // retractSending drops a message whose send failed.
  const retractSending = (rowKey: string, promptId: string) =>
    setSendingByRow((previous) => {
      const entries = previous.get(rowKey);
      if (entries === undefined || !entries.some((entry) => entry.promptId === promptId)) {
        return previous;
      }
      return new Map(previous).set(rowKey, entries.filter((entry) => entry.promptId !== promptId));
    });
  // dropTaken removes the texts an Escape took back from the row: its sending entries, its held messages on each of its
  // streams, and the local queued entry each of its composers holds. A stream's next delta then replaces its held set
  // with the server's own.
  const dropTaken = (key: string, texts: string[]) => {
    setSendingByRow((previous) => withoutSent(previous, key, texts));
    const dropHeld = (current: Stream | null): Stream | null => {
      if (current === null || current.key !== key) {
        return current;
      }
      const held = withoutTexts(current.conversation.held, texts);
      return { ...current, conversation: { ...current.conversation, held } };
    };
    setStream(dropHeld);
    setPinStream(dropHeld);
    composerHandles.current.get(key)?.forget(texts);
    pinnedHandles.current.get(key)?.forget(texts);
  };
  // escapeRow takes back a row's queued messages: their texts go to the column's composer after the text it holds, and
  // the row's sending and held entries for them are dropped. The row is then interrupted when it was working at the
  // press. A failed take back shows its line, and the interrupt still goes out. The composer keeps focus when it
  // interrupted or took messages back, and otherwise leaves it, as Escape does.
  const escapeRow = async (target: EscapeTarget, box: HTMLElement | null): Promise<void> => {
    const { row, column, state } = target;
    const interrupting = state === stateWorking;
    const failures: string[] = [];
    let texts: string[] = [];
    try {
      texts = await actions.withdraw(clients, row);
    } catch {
      failures.push(takeBackFailedText);
    }
    if (texts.length > 0) {
      dropTaken(row.key, texts);
      (column === "pinned" ? pinnedHandles : composerHandles).current.get(row.key)?.append(texts);
    }
    if (interrupting) {
      try {
        await actions.interrupt(clients, row);
      } catch {
        failures.push(interruptFailedText);
      }
    }
    setEscapeError(failures.join(" "));
    if (box !== null && !interrupting && texts.length === 0) {
      box.blur();
    }
  };
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

  // A notice's landing opens the epic page on the block it names. Effects run in declaration order, and this one is
  // declared after the cursor's reset above, so in the commit where a notice moves the cursor the landing is the page
  // the reset leaves. A contract landing opens the contract page over the epic page, as its summary does.
  useLayoutEffect(() => {
    if (landing === null) {
      return;
    }
    if (landing.kind === contractBlock) {
      setEpicPage(openEpicPage);
      openContract();
      return;
    }
    setEpicPage({ ...openEpicPage, landing });
  }, [landing]);

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

  // The palette's answered row takes the cursor once the loaded rows hold it, so a row that arrives in a later render
  // is reached too. A filter that hides the row is accepted: the cursor then stays where it is.
  useEffect(() => {
    if (pending === null) {
      return;
    }
    const found = rows.find((row) => row.kind === pending.kind && row.id === pending.id);
    if (found === undefined) {
      return;
    }
    setPending(null);
    const target = flat.findIndex((item) => item.key === found.key);
    if (target >= 0) {
      moveTo(target);
    }
  }, [pending, rows, flat]);

  // openEpic moves the cursor to an epic's entry, first opening the accepted or ended earlier line that holds it when
  // that line is shut. The entry is then in the list, so the cursor rests on it.
  const openEpic = (id: string) => {
    if (marked.waiting.some((item) => item.id === id)) {
      setWaitingOpen(true);
    }
    if (kept.endedEarlier.some((item) => item.id === id)) {
      setEndedOpen(true);
    }
    setCursor({ key: `epic:${id}`, index: shownIndex });
  };

  // markNoticeSeen sends the notice's key as seen. A failed report is dropped; the notice stays until it ends.
  const markNoticeSeen = (notice: InAppNotice) => {
    void clients.notices.seen({ key: notice.key }).catch(() => undefined);
  };

  // openNotice counts a notice seen, then lands on what it names. An epic moves the cursor to its entry, wherever the
  // list holds it, and lands its page on the block the notice names. A pane or a session moves the cursor to its row or
  // step, when the list shows it. A failed report is dropped; the notice stays in the banner until it ends.
  const openNotice = (notice: InAppNotice) => {
    markNoticeSeen(notice);
    const epic = notice.key?.epic ?? "";
    if (epic !== "") {
      if (listsEpic(kept, epic)) {
        openEpic(epic);
        setLanding({ kind: notice.block?.kind ?? "" });
      }
      return;
    }
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

  // closePin closes the pinned reader, as | does on a pin.
  const closePin = () => {
    setPinned(null);
    setPinStream(null);
  };

  // focusCursorRow moves focus to the list's current row, the row the cursor rests on.
  const focusCursorRow = () => {
    document.querySelector<HTMLElement>(".b-row.cur")?.focus();
  };

  // replyBoxTarget is the row whose reply box is focused, when it is the cursor's or the pinned row's, with the row's
  // state at the press. A step's box, the talk dock's box and any other box are none.
  const replyBoxTarget = (box: HTMLElement): EscapeTarget | null => {
    if (cursorRow !== null && composerInputs.current.get(cursorRow.key) === box) {
      return { row: cursorRow, column: "cursor", state: headState(cursorRow, cursorConversation?.state?.pane) };
    }
    if (pinned !== null && pinnedInputs.current.get(pinned.key) === box) {
      return { row: pinned, column: "pinned", state: headState(pinned, pinnedConversation?.state?.pane) };
    }
    return null;
  };

  const handleKey = (event: KeyboardEvent) => {
    // ⌘K and Ctrl+K open the palette anywhere, a text box included.
    if ((event.metaKey || event.ctrlKey) && !event.altKey && event.key.toLowerCase() === "k") {
      event.preventDefault();
      setPickerOpen(true);
      return;
    }
    if (event.ctrlKey || event.metaKey || event.altKey) {
      return;
    }
    // While the palette is open it owns the keys: Escape closes it, and every other key is left to the palette's own
    // controls, so the list keeps its keys and a focused row or button keeps its native Enter.
    if (pickerOpen) {
      if (event.key === "Escape") {
        setPickerOpen(false);
        event.preventDefault();
      }
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
    // Keys typed in a text box are text: only Escape leaves it. That covers the reply boxes and the answer box. Escape
    // in the cursor's or the pinned row's reply box takes its queued messages back, as Escape outside a box does; any
    // other box is only left.
    const focused = document.activeElement;
    if (focused instanceof HTMLElement && (focused.tagName === "TEXTAREA" || focused.tagName === "INPUT")) {
      if (event.key === "Escape") {
        const target = replyBoxTarget(focused);
        if (target === null) {
          focused.blur();
        } else {
          void escapeRow(target, focused);
        }
      }
      return;
    }
    // A focused button keeps its own Enter, so + Ask opens the palette and Enter writes no seen.
    if (event.key === "Enter" && focused instanceof HTMLElement && focused.tagName === "BUTTON") {
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
      case "v":
        setView("archived");
        break;
      case "A":
        // A pane has no archive kind, and a step is never archived, so the key does nothing on either.
        if (cursorItem !== null && cursorItem.kind !== "step") {
          setArchive(cursorItem, true);
        }
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
      case "N":
        setPickerOpen(true);
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
          closePin();
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
        if (!helpOpen && cursorRow !== null) {
          void escapeRow(
            { row: cursorRow, column: "cursor", state: headState(cursorRow, cursorConversation?.state?.pane) },
            null,
          );
        }
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
        <div>
          <TitleRow
            crumbs={[
              { label: "orrery", onGo: closeEpicPage },
              { label: detail.epic?.slug ?? "", onGo: toEpicPage },
              { label: "contract" },
            ]}
            onBack={back}
          />
          <ContractBody contract={detail.contract} requirements={detail.requirements} />
        </div>
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
          onOlder={() => sessionOlder.current()}
          nudge={calls.nudge}
          hold={calls.hold}
          stop={calls.stop}
        />
      );
    }
    return (
      <EpicPage
        detail={detail}
        landing={epicPage.landing}
        history={epicHistory.id === entry.id ? epicHistory.entries : []}
        now={now}
        loadArtifact={loadArtifact}
        commands={epicCommands(clients, entry.id, setCommandError)}
        numberKeys={!epicPage.closed}
        onOpenContract={openContract}
        onOpenPhase={openPhase}
        onBack={closeEpicPage}
        handover={Handover({ detail })}
        actions={
          <Talk
            key={entry.id}
            epic={entry.id}
            sessions={detail.sessions}
            repository={detail.repository}
            start={(dir, prompt) => clients.control.start({ dir, prompt }).then((reply) => reply.id)}
            onOpen={(target) => setTalkDock({ epic: entry.id, target })}
          />
        }
      />
    );
  };

  return (
    <main class="app">
      <div class={pinned !== null ? "panes split" : "panes"}>
        {notices.length > 0 && <NoticeBanner notices={notices} onOpen={openNotice} onDismiss={markNoticeSeen} />}
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
              <button type="button" onClick={() => setPickerOpen(true)}>
                + Ask
              </button>
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
              <span class={view === "archived" ? "view on" : "view"} onClick={() => setView("archived")}>
                <kbd>v</kbd>Archived
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
            {escapeError !== "" && (
              <p class="error" role="alert">
                {escapeError}
              </p>
            )}
            {archiveError !== "" && (
              <p class="error" role="alert">
                {archiveError}
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
                  <i>{section.title === needsYouTitle ? section.count + needs.length : section.count}</i>
                </div>
                {section.epics.map((block) => (
                  <Fragment key={block.epic.key}>
                    <EpicLine
                      epic={block.epic}
                      full={block.full}
                      current={block.epic.key === cursorKey}
                      now={now}
                      ended={view === "archived" ? archivedEnded(block.epic, now) : undefined}
                      archive={archiveControl(block.epic)}
                      onSelect={selectItem}
                    />
                    {block.steps.map((step) => (
                      <StepEntry key={step.key} step={step} current={step.key === cursorKey} onSelect={selectItem} />
                    ))}
                    {block.more > 0 && <div class="b-more">{`${block.more} more`}</div>}
                    {block.tail !== "" && <div class="b-more">{block.tail}</div>}
                  </Fragment>
                ))}
                {section.justEnded.map((epic) => (
                  <EpicLine
                    key={epic.key}
                    epic={epic}
                    full={false}
                    current={epic.key === cursorKey}
                    now={now}
                    ended={endedWords(epic, now)}
                    archive={archiveControl(epic)}
                    onSelect={selectItem}
                  />
                ))}
                {section.endedEarlier.length > 0 && (
                  <div class="b-more" onClick={() => setEndedOpen((value) => !value)}>
                    {`${section.endedEarlier.length} ended earlier ›`}
                  </div>
                )}
                {endedOpen &&
                  section.endedEarlier.map((epic) => (
                    <EpicLine
                      key={epic.key}
                      epic={epic}
                      full={false}
                      current={epic.key === cursorKey}
                      now={now}
                      ended={endedWords(epic, now)}
                      archive={archiveControl(epic)}
                      onSelect={selectItem}
                    />
                  ))}
                {section.waiting.length > 0 && (
                  <div class="b-more" onClick={() => setWaitingOpen((value) => !value)}>
                    {`${section.waiting.length} accepted, waiting to start`}
                  </div>
                )}
                {waitingOpen &&
                  section.waiting.map((epic) => (
                    <EpicLine
                      key={epic.key}
                      epic={epic}
                      full={false}
                      current={epic.key === cursorKey}
                      now={now}
                      archive={archiveControl(epic)}
                      onSelect={selectItem}
                    />
                  ))}
                {section.rows.map((item) => (
                  <div
                    key={item.key}
                    class={item.key === cursorRowKey ? "b-row cur" : "b-row"}
                    tabIndex={-1}
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
                        {item.archiveKind !== "none" && <ArchiveButton control={archiveControl(item)} />}
                      </div>
                      <div class={item.unseen ? "l2" : "l2 one"}>
                        <ReplyText text={item.state === stateWaiting ? rowNeed(item).text : item.summary} />
                      </div>
                      {item.state === stateWaiting && item.summary !== "" && (
                        <div class="l2 one why">
                          <ReplyText text={item.summary} />
                        </div>
                      )}
                      <RowOptions
                        row={item}
                        conversation={heldConversation(item.key)}
                        answer={answered.get(item.key)}
                      />
                    </div>
                  </div>
                ))}
                {section.title === needsYouTitle &&
                  needs.map((need) => (
                    <NeedEntry
                      key={`${need.kind}:${need.epic}:${need.question}`}
                      need={need}
                      slug={epics.details.get(need.epic)?.epic?.slug ?? ""}
                      commands={epicCommands(clients, need.epic, setCommandError)}
                      onOpen={() => openEpic(need.epic)}
                    />
                  ))}
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
            <ConversationHead
              label={cursorRow.label}
              state={headState(cursorRow, cursorConversation?.state?.pane)}
              onBack={focusCursorRow}
              onGoList={focusCursorRow}
            >
              <Pill model={rowPill(cursorRow.state, cursorConversation?.state?.pane)} now={now} />
            </ConversationHead>
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
              onOlder={() => cursorOlder.current()}
              sending={cursorSending}
            />
          )}
          {cursorStep !== null && (
            <Reader
              conversation={cursorConversation}
              seenAt={stepSeenAt}
              open={open}
              step={cursorStep.step}
              onToggleOpen={toggleOpen}
              onOlder={() => cursorOlder.current()}
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
                  register={(handle) => registerHandle(composerHandles, row.key, handle)}
                  queued={row.key === cursorRowKey ? cursorConversation?.state?.waiting : undefined}
                  held={row.key === cursorRowKey ? cursorConversation?.held : undefined}
                  onSending={(sent) => addSending(row.key, sent, row.key === cursorRowKey ? cursorConversation : null)}
                  onUnsent={(promptId) => retractSending(row.key, promptId)}
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
            <ConversationHead
              label={pinned.label}
              state={headState(pinned, pinnedConversation?.state?.pane)}
              onBack={closePin}
              onGoList={focusCursorRow}
            >
              <Pill model={rowPill(pinned.state, pinnedConversation?.state?.pane)} now={now} />
            </ConversationHead>
          )}
          {pinned !== null && (
            <Reader
              conversation={pinnedConversation}
              seenAt={pinned.seen}
              open={false}
              onOlder={() => pinnedOlder.current()}
              sending={pinnedSending}
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
                  register={(handle) => registerHandle(pinnedHandles, row.key, handle)}
                  queued={row.key === pinnedKey ? pinnedConversation?.state?.waiting : undefined}
                  held={row.key === pinnedKey ? pinnedConversation?.held : undefined}
                  onSending={(sent) => addSending(row.key, sent, row.key === pinnedKey ? pinnedConversation : null)}
                  onUnsent={(promptId) => retractSending(row.key, promptId)}
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
          class={pageClasses(epicPage.move, docked !== null)}
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
          {docked !== null && (
            <TalkDock clients={clients} target={docked.target} onClose={() => setTalkDock(null)} />
          )}
        </section>
      </div>
      {pickerOpen && (
        <PickerPalette
          clients={clients}
          onRun={(pane) => {
            setPickerOpen(false);
            setPending({ kind: "pane", id: pane });
          }}
          onStart={(session) => {
            setPickerOpen(false);
            setPending({ kind: "session", id: session });
          }}
          onClose={() => setPickerOpen(false)}
        />
      )}
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

// endingKey names one ending of an ended epic: its line's key and the time it ended. A later ending has a new key.
function endingKey(epic: EpicItem): string {
  return `${epic.key}@${epic.phaseAt}`;
}

// readEpicsAsSeen moves each just-ended epic whose ending a seen write landed for into endedEarlier, newest ending
// first, as the epics view draws an epic its poll reads as seen.
function readEpicsAsSeen(view: EpicsView, written: Set<string>): EpicsView {
  const isWritten = (epic: EpicItem) => written.has(endingKey(epic));
  return {
    ...view,
    justEnded: view.justEnded.filter((epic) => !isWritten(epic)),
    endedEarlier: [...view.endedEarlier, ...view.justEnded.filter(isWritten)].sort(newestEnding),
  };
}

// newestEnding orders ended epics newest ending first.
function newestEnding(a: EpicItem, b: EpicItem): number {
  return b.phaseAt - a.phaseAt;
}

// withRowMark applies the archive mark a landed call left to a row. A row with no mark is returned as it is.
function withRowMark(row: Row, marks: Map<string, boolean>): Row {
  const mark = marks.get(row.key);
  return mark === undefined ? row : { ...row, archived: mark };
}

// withEpicMarks applies the archive marks the landed calls left to an epics view. A marked epic moves into archived,
// out of the list that held it. An epic marked unarchived moves out of archived into the list its record places it in,
// as epicsView places a live or ended epic whose detail is held, and into Ended earlier when no detail is. The next
// list answer places it exactly. A view with no marks is returned as it is.
function withEpicMarks(view: EpicsView, marks: Map<string, boolean>): EpicsView {
  if (marks.size === 0) {
    return view;
  }
  const archivedNow = (item: EpicItem) => marks.get(item.id) ?? item.archived;
  const archive = (item: EpicItem): EpicItem => ({ ...item, archived: true });
  const blocks = view.blocks.filter((block) => !archivedNow(block.epic));
  const waiting = view.waiting.filter((item) => !archivedNow(item));
  const justEnded = view.justEnded.filter((item) => !archivedNow(item));
  const endedEarlier = view.endedEarlier.filter((item) => !archivedNow(item));
  const archived = [
    ...view.blocks.filter((block) => archivedNow(block.epic)).map((block) => archive(block.epic)),
    ...view.waiting.filter(archivedNow).map(archive),
    ...view.justEnded.filter(archivedNow).map(archive),
    ...view.endedEarlier.filter(archivedNow).map(archive),
    ...view.archived.filter(archivedNow),
  ];
  for (const item of view.archived) {
    if (archivedNow(item)) {
      continue;
    }
    const detail = item.detail;
    if (detail?.epic === undefined) {
      endedEarlier.push({ ...item, archived: false });
      continue;
    }
    const placed = epicsView([{ ...detail.epic, archived: false }], new Map([[detail.epic.id, detail]]));
    blocks.push(...placed.blocks);
    waiting.push(...placed.waiting);
    justEnded.push(...placed.justEnded);
    endedEarlier.push(...placed.endedEarlier);
  }
  return {
    blocks: [...blocks.filter((block) => block.full), ...blocks.filter((block) => !block.full)],
    waiting,
    live: blocks.length + waiting.length,
    justEnded: justEnded.sort(newestEnding),
    endedEarlier: endedEarlier.sort(newestEnding),
    archived,
    details: view.details,
  };
}

// archivedSection is the Archived view's one section: each archived epic as a line, then each archived row.
function archivedSection(epics: EpicItem[], rows: Row[]): EpicSection {
  return {
    title: archivedTitle,
    epics: epics.map((item): EpicBlock => ({ epic: item, full: false, steps: [], more: 0, tail: "" })),
    waiting: [],
    rows,
    justEnded: [],
    endedEarlier: [],
    count: epics.length + rows.length,
  };
}

// archivedEnded is the ended words an archived epic's line draws when its detail does not say it is live: its outcome
// and the time since it ended. A live epic's line draws its state from its detail, so it draws none.
function archivedEnded(item: EpicItem, now: number): string | undefined {
  return item.detail?.epic?.live === true ? undefined : endedWords(item, now);
}

// lineWrite runs one write whose failure shows a fixed line. A failure sets the line to failed and runs no landed; a
// write that lands clears the line and runs landed.
async function lineWrite(
  write: () => Promise<unknown>,
  failed: string,
  setLine: (text: string) => void,
  landed: () => void,
): Promise<void> {
  try {
    await write();
  } catch {
    setLine(failed);
    return;
  }
  setLine("");
  landed();
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

// pruneSent drops the sent entries of each row whose conversation is held, and returns previous itself when none is sent.
function pruneSent(
  previous: Map<string, Sending[]>,
  held: { key: string; kind: SendKind; conversation: Conversation | null }[],
): Map<string, Sending[]> {
  let next = previous;
  for (const { key, kind, conversation } of held) {
    const entries = previous.get(key);
    if (entries === undefined) {
      continue;
    }
    const kept = unsentEntries(entries, conversation, kind);
    if (kept === entries) {
      continue;
    }
    if (next === previous) {
      next = new Map(previous);
    }
    if (kept.length === 0) {
      next.delete(key);
    } else {
      next.set(key, kept);
    }
  }
  return next;
}

// registerHandle keeps a composer's handle under its row key while the composer is mounted, and drops it on null.
function registerHandle(handles: { current: Map<string, ComposerHandle> }, key: string, handle: ComposerHandle | null) {
  if (handle === null) {
    handles.current.delete(key);
  } else {
    handles.current.set(key, handle);
  }
}

// withoutSent drops the texts from the sending entries of one row, and returns previous itself when none is dropped.
function withoutSent(previous: Map<string, Sending[]>, key: string, texts: string[]): Map<string, Sending[]> {
  const entries = previous.get(key);
  if (entries === undefined) {
    return previous;
  }
  const kept = withoutTexts(entries, texts);
  if (kept === entries) {
    return previous;
  }
  const next = new Map(previous);
  if (kept.length === 0) {
    next.delete(key);
  } else {
    next.set(key, kept);
  }
  return next;
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

// noticeEntry finds the row or step a notice's key names among the entries the list shows: a pane row by pane id, or a
// session row or a step by session id. A key that names none, such as a limit's or an epic's, finds nothing.
function noticeEntry(items: Item[], key: NoticeKey | undefined): Item | undefined {
  const pane = key?.pane ?? "";
  const session = key?.session ?? "";
  return items.find((item) => {
    if (item.kind === "pane") {
      return pane !== "" && item.id === pane;
    }
    return item.kind !== "epic" && session !== "" && item.id === session;
  });
}

// listsEpic reports whether the view holds the epic under id in any of its lines, open or shut.
function listsEpic(view: EpicsView, id: string): boolean {
  const held = [
    ...view.blocks.map((block) => block.epic),
    ...view.waiting,
    ...view.justEnded,
    ...view.endedEarlier,
  ];
  return held.some((item) => item.id === id);
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
    answer: (question, choice, text) =>
      clients.epics.answer({ epicId, id: crypto.randomUUID(), text, question, choice }).then(
        () => "",
        (error: unknown) => (error instanceof Error && error.message !== "" ? error.message : answerFailedText),
      ),
  };
}

// entriesOf lists a section's entries in the order they are drawn: each epic with its steps, then the just-ended epics,
// then the seen ones while their line is open, then the waiting epics while their line is open, then the rows.
function entriesOf(section: EpicSection, waitingOpen: boolean, endedOpen: boolean): Item[] {
  return [
    ...section.epics.flatMap((block) => [block.epic, ...block.steps]),
    ...section.justEnded,
    ...(endedOpen ? section.endedEarlier : []),
    ...(waitingOpen ? section.waiting : []),
    ...section.rows,
  ];
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

// withNeedsSection adds a Needs you section first when epic needs call for one and no waiting row has made it. Its rows
// are none; the epic needs draw in it.
function withNeedsSection(sections: Section[], hasNeeds: boolean): Section[] {
  if (!hasNeeds || sections.some((section) => section.title === needsYouTitle)) {
    return sections;
  }
  return [{ title: needsYouTitle, rows: [] }, ...sections];
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

// stepWhere is a step's where line: its epic, its agent, and its round.
function stepWhere(step: StepItem): string {
  return `${step.epicSlug} › ${step.agent} · step · round ${step.round}`;
}

// pageClasses are the classes of the epic page section: its move, and docked while a conversation docks beside it.
function pageClasses(move: EpicPageState["move"], docked: boolean): string {
  return ["page", move, docked ? "docked" : ""].filter((name) => name !== "").join(" ");
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

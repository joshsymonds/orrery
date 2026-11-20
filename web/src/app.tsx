import { Code, ConnectError } from "@connectrpc/connect";
import { Fragment } from "preact";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks";

import * as actions from "./actions";
import type { Clients } from "./actions";
import { askingWords, askLines, headBadges, ownAsks, steeredEpics } from "./asks";
import { epicCallsOf } from "./card";
import type { DockHandle } from "./dock";
import { ContractBody } from "./contract";
import { type Conversation, shownConversation } from "./conversation";
import { type ArchiveControl, EpicLine } from "./epicline";
import { type EpicCommands, EpicPage, type Landing, type SessionTarget } from "./epicpage";
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
import { findMatches } from "./find";
import { type Followed, follow } from "./follow";
import type { EpicHistoryEntry, EpicsGetResponse, EpicStep } from "./gen/orrery/v1/epics_pb";
import type { InAppNotice, NoticeKey } from "./gen/orrery/v1/notice_pb";
import type { HostStatusResponse } from "./gen/orrery/v1/orrery_pb";
import { Handover } from "./handover";
import { type HeadStore, memoryHeadStore } from "./headfold";
import { KeyList } from "./helppanel";
import { type Answer, InboxRow, isLinkClick, optionText, readerQuestion, stateWaiting } from "./inboxrow";
import {
  type BoxOpen,
  type EscapeTarget,
  handleKey,
  isRow,
  type Item,
  type KeyContext,
  type Popout,
  type View,
} from "./keymap";
import { listHolds, listsEpic } from "./listholds";
import { Mark } from "./mark";
import { NeedEntry } from "./needentry";
import { epicNeeds } from "./needs";
import { NoticeBanner } from "./notices";
import { PhaseOutputPage } from "./phaseoutput";
import { phaseOutputOf } from "./phaserows";
import type { Phase } from "./phases";
import { NewPanel } from "./picker";
import { type Viewing, viewingOf } from "./presence";
import type { ReaderHandle } from "./reader";
import { type Address, type Entry, formatAddress, type ListPanelKind, type PageView } from "./router";
import { byAttention, byProject, type Row, type Section, splitArchived } from "./rows";
import { type SeenClock, type SeenWindow, watchSeen } from "./seen";
import { type SendKind, type Sending, type SentText, sendingEntry, unsentEntries, withoutTexts } from "./sending";
import { type ColumnHead, type FindState, headState, ReaderColumn, stepCalls } from "./readercolumn";
import { Sheet } from "./sheet";
import { ListPanel, listHead, SidePanel } from "./sidepanel";
import { steerCallsOf, steererRowOf } from "./steer";
import { SteerMenu, useSteer } from "./steermenu";
import { PaneStrip, type StripHost } from "./strip";
import { ReplyText } from "./text";
import { lastPathElement } from "./titleparts";
import { type InboxMark, TitleRow } from "./titlerow";
import { type ListThing, type Thing, type WorkConversation, workOf } from "./work";
import { WorkerSessionPage } from "./workersession";
import { WorkRail } from "./workrail";

// The inbox's state: pinned beside the page, docked beside it while no row has been opened, hidden once one has, or
// popped out over the hidden page while the pointer is on the mark or a key has popped it out.
type InboxState = "docked" | "pinned" | "hidden" | "flyout";

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

// A steering move's answer: the epic, the steering the list held when the move was made, and the session the re-read
// named.
interface Steered {
  epic: string;
  listed: string;
  session: string;
}

// A step a card opened: the epic that holds it and the step's id. The page opens on the step once the epic's detail holds
// the step.
interface SteppedTo {
  epic: string;
  step: string;
}

// An epic entry an opened address names. It is wrapped in a fresh object for each open, so an open of the same entry
// again still changes the state the layout effect that applies its sub-page reads.
interface OpenedEpic {
  entry: Extract<Entry, { kind: "epic" }>;
}

// A pending open is the entry an opened address names, with the order it names. The entry is decided only in a pass whose
// view is that order.
interface PendingOpen {
  order: View;
  entry: Entry;
}

// A list panel open beside a session: the key of the session's row it is open on, and the list it shows.
interface ListOpen {
  key: string;
  panel: ListPanelKind;
}

// What a panel held, which ] reopens once no panel is open: an epic by its id, or a session's list by its open state.
type Held = { kind: "epic"; id: string } | { kind: "list"; open: ListOpen };

// The row the palette answered: the pane Picker.Run answered, or the session Control.Start started, by its kind and id.
interface PendingTarget {
  kind: Row["kind"];
  id: string;
}

// The note a page's More menu left under a row's title row, which names the row it is for.
interface HeadNote {
  key: string;
  text: string;
}

const noHeadNote: HeadNote = { key: "", text: "" };

const stateWorking = "working";
// The block kind a notice names when it opens the contract page.
const contractBlock = "contract";
const needsYouTitle = "Needs you";
const newSinceTitle = "New since you looked";
const noDirectoryTitle = "no directory";
const epicUnavailableText = "epic unavailable";
const noSteererText = "No conversation steers this epic.";
const seenFailedText = "could not mark seen";
const answerFailedText = "no answer was sent";
const takeBackFailedText = "could not take the queued messages back";
const interruptFailedText = "could not interrupt";
const commandFailedText = "epic command failed";
const sessionUnavailableText = "session unavailable";
const archiveFailedText = "could not change archive";
const noAnswerText = "the server did not answer";
// The codes a call's failure reads as the server not answering.
const noAnswerCodes = new Set([Code.Unavailable, Code.DeadlineExceeded, Code.Unknown]);
const archivedTitle = "Archived";
// The most entries one page of an epic's history asks for.
const historyPageSize = 200;
const heldMark = "‖";

// The query of the wide layout, the same one style.css's wide block uses. Only that layout hides, pops out or pins the
// inbox: a narrower window always shows it docked, as the stacked layout does.
const wideQuery = "(min-width: 760px)";

// The query of the layout at 1250px and under, where the epic panel lies over the conversation from the right, beside the
// rail, rather than taking a track of its own.
const overlayQuery = "(max-width: 1250px)";

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
  address,
  onAddress,
  headStore,
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
  address?: Address;
  onAddress?: (address: Address, mode: "push" | "replace") => void;
  headStore?: HeadStore;
}) {
  const [view, setView] = useState<View>("attention");
  const [cursor, setCursor] = useState<Cursor>({ key: "", index: 0 });
  const [filter, setFilter] = useState("");
  const [helpOpen, setHelpOpen] = useState(false);
  const [open, setOpen] = useState(false);
  // Whether the strips under the title rows are drawn. m hides and shows every one of them.
  const [stripsShown, setStripsShown] = useState(true);
  // Whether a row has been opened by a click or Enter, which hides the inbox. The mark's click or B pins the inbox
  // beside the page. A hidden inbox is popped out over the page by the pointer on the mark, or by a key, and popout
  // records which.
  const [rowOpened, setRowOpened] = useState(false);
  const [inboxPinned, setInboxPinned] = useState(false);
  const [popout, setPopout] = useState<Popout>("none");
  // Whether the window is wide, read at mount and followed on the query's change event. A window narrowed below 760px
  // drops any pop-out, and keeps rowOpened and the pin, so widening it again shows the inbox as it was.
  const [wide, setWide] = useState(() => window.matchMedia(wideQuery).matches);
  useEffect(() => {
    const query = window.matchMedia(wideQuery);
    const follow = () => {
      setWide(query.matches);
      if (!query.matches) {
        setPopout("none");
      }
    };
    query.addEventListener("change", follow);
    return () => {
      query.removeEventListener("change", follow);
    };
  }, []);
  // Whether the window is at 1250px or narrower, read at mount and followed on the query's change event.
  const [overlay, setOverlay] = useState(() => window.matchMedia(overlayQuery).matches);
  useEffect(() => {
    const query = window.matchMedia(overlayQuery);
    const sync = () => setOverlay(query.matches);
    query.addEventListener("change", sync);
    return () => {
      query.removeEventListener("change", sync);
    };
  }, []);
  const listRef = useRef<HTMLDivElement>(null);
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
  const composerHandles = useRef(new Map<string, DockHandle>());
  const pinnedHandles = useRef(new Map<string, DockHandle>());
  // The handles of the cursor's and the pinned column's readers. An empty reply box's Page Up and Page Down page them.
  const cursorReader = useRef<ReaderHandle | null>(null);
  const pinnedReader = useRef<ReaderHandle | null>(null);
  // The row | pinned beside the list, with its own stream. The rows that have been pinned each keep a composer, as the
  // visited rows do, so a draft or a failed send survives the pin's close and its reopening on that row.
  const [pinned, setPinned] = useState<Row | null>(null);
  const [pinStream, setPinStream] = useState<Stream | null>(null);
  const [pinnedRows, setPinnedRows] = useState<Row[]>([]);
  const pinnedInputs = useRef(new Map<string, HTMLTextAreaElement>());
  // The find bars of the cursor's and the pinned column, each open on the row it was opened on. null is a shut bar.
  const [cursorFind, setCursorFind] = useState<FindState | null>(null);
  const [pinnedFind, setPinnedFind] = useState<FindState | null>(null);
  // The reply box an opening asks to focus: a click or Enter on a row, or | on a row. The layout effect takes it once the
  // render that draws the box has run.
  const [boxOpened, setBoxOpened] = useState<BoxOpen | null>(null);
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
  // The headless session rows the owner has interrupted, by row key, each with the turn_since its stream held at the press
  // (0 when no stream held one). A marked row reads interrupting on its dock's line while the list still says it is
  // working. The mark clears once the row's stream reports interrupting or a turn other than the one held, or once the
  // list no longer says working.
  const [interrupts, setInterrupts] = useState<Map<string, number>>(() => new Map());
  const [archiveError, setArchiveError] = useState("");
  // The per-device store the hidden header is kept in, read once, and whether the header is hidden now. The note a More
  // menu's archive left is under the title row of the row it names.
  const [keptHead] = useState<HeadStore>(() => headStore ?? memoryHeadStore());
  const [headHidden, setHeadHidden] = useState(() => keptHead.hidden());
  const [headNote, setHeadNote] = useState<HeadNote>(noHeadNote);
  // The key of the entry whose ⋯ menu is open, if any: one menu is open at a time. It counts as open only while the entry
  // is drawn.
  const [menuKey, setMenuKey] = useState<string | null>(null);
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
  // The session a steering move of an epic named when its re-read answered, with the epic and the steering the list
  // held then. The middle row keeps naming it until the list's own steering of the epic changes.
  const [steered, setSteered] = useState<Steered | null>(null);
  // The landing the last opened notice names. Each notice makes its own, so its effect applies it once.
  const [landing, setLanding] = useState<Landing | null>(null);
  // The step a card opened, whose page lands once the epic's detail holds it.
  const [steppedTo, setSteppedTo] = useState<SteppedTo | null>(null);
  // The epic entry the last opened address named, whose sub-page the page lands on in the commit that moves the cursor.
  const [opened, setOpened] = useState<OpenedEpic | null>(null);
  // The worker session the page shows, held under the step it belongs to.
  const [sessionStream, setSessionStream] = useState<Stream | null>(null);
  // The list panel open beside a session, and the thing a panel held last, which ] reopens.
  const [listPanel, setListPanel] = useState<ListOpen | null>(null);
  const [held, setHeld] = useState<Held | null>(null);
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

  // The address App reports. lastForm is the form it last reported or applied. cause is the mode the next address change
  // takes: push for an open, a crumb return or the first cursor key after one, replace for a later cursor key. A cause
  // is set together with a render of its own, and the report of that render consumes it, so a cause that changes no
  // address is never carried to a later change. armed is whether the next cursor key pushes, and engaged whether any
  // cause has come since mount, which a change with no cause waits for. applied is set when an address is applied, and
  // the report after it takes the form App shows without reporting it. pendingOpen is the entry an address names, with its
  // order, until it is applied or dropped, and pageCursor is the cursor key the epic page was last reset for.
  const lastForm = useRef<string | null>(null);
  const cause = useRef<"push" | "replace" | null>(null);
  const armed = useRef(false);
  const engaged = useRef(false);
  const applied = useRef(false);
  const [, setAddressTick] = useState(0);
  const addressMounted = useRef(false);
  const pendingOpen = useRef<PendingOpen | null>(null);
  const pageCursor = useRef<string | null>(null);

  // renderAddress guarantees a render after a cause or an applied address, whether or not the action changes any state.
  // Without it, a cause whose action changes nothing, such as h at the inbox, would reach no render, and the next
  // unrelated change would consume it.
  const renderAddress = () => setAddressTick((tick) => tick + 1);
  // arm arms the next cursor key to push, and engages the reports: a change with no cause is reported from then on.
  const arm = () => {
    armed.current = true;
    engaged.current = true;
  };
  // navigated wraps an open or a crumb return: the address change it makes pushes, and it arms the next cursor key.
  const navigated =
    <A extends unknown[]>(action: (...args: A) => void) =>
    (...args: A) => {
      cause.current = "push";
      arm();
      renderAddress();
      action(...args);
    };
  // keyMoved wraps a cursor key's move: the first cursor key after an arm pushes and disarms, and the later ones
  // replace.
  const keyMoved =
    <A extends unknown[]>(action: (...args: A) => void) =>
    (...args: A) => {
      cause.current = armed.current ? "push" : "replace";
      armed.current = false;
      engaged.current = true;
      renderAddress();
      action(...args);
    };
  // adopt records an applied address. It reports nothing, and arms the next cursor key. Its render is the one whose
  // report takes the applied form, so that form is never left waiting for a later change.
  const adopt = () => {
    applied.current = true;
    arm();
    renderAddress();
  };

  const words = filter
    .toLowerCase()
    .split(/\s+/)
    .filter((word) => word !== "");
  const shownRows = rows.map((row) => readAsSeen(withRowMark(row, rowMarks), seenWritten));
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
  // The live epics' needs, which the mark counts in every view. Only the attention view draws them in its section.
  const liveNeeds = epicNeeds(detailEpics, epics.details);
  const needs = view === "attention" ? liveNeeds : [];
  // The mark's count: the loaded rows that need you and are not archived, and the live epics' needs. It reads the same
  // in every view and under any filter, as the list reads every loaded row after seen writes.
  const markNeeds =
    splitArchived(shownRows).rows.filter((row) => row.state === stateWaiting).length + liveNeeds.length;
  // Under 760px the inbox is docked, whatever the row, the pin and the pop-out hold.
  const inboxState: InboxState = !wide
    ? "docked"
    : inboxPinned
      ? "pinned"
      : !rowOpened
        ? "docked"
        : popout !== "none"
          ? "flyout"
          : "hidden";
  // The inbox is hidden once a row has been opened, unless it is pinned or popped out. A key pop-out is the flyout that
  // Escape and b tuck back.
  const inboxHidden = inboxState === "hidden";
  const keyPopped = inboxState === "flyout" && popout === "key";
  const sections =
    view === "attention"
      ? withEpicsGroup(withNeedsSection(byAttention(defaultRows), needs.length > 0), kept)
      : view === "project"
        ? placeEpics(byProject(defaultRows), kept.blocks)
        : [archivedSection(kept.archived, archivedRows)];
  const flat = sections.flatMap((section) => entriesOf(section, waitingOpen, endedOpen));
  // The open ⋯ menu's entry, when the list draws it: a menu whose entry is not drawn counts as closed.
  const menuDrawn = menuKey !== null && flat.some((item) => item.key === menuKey);
  const openMenu = menuDrawn ? menuKey : null;
  const index = resolveIndex(flat, cursor);
  const cursorItem = index >= 0 ? flat[index] : null;
  // The cursor's row, which an epic or step entry never has: the composer and seen watch follow only a row.
  const cursorRow = cursorItem !== null && isRow(cursorItem) ? cursorItem : null;
  const cursorKey = cursorItem?.key ?? "";
  const cursorRowKey = cursorRow?.key ?? "";
  // The cursor's step, whose reader follows its session and whose step box it shows. An epic entry is no step.
  const cursorStep = cursorItem?.kind === "step" ? cursorItem : null;
  const cursorStepKey = cursorStep?.key ?? "";
  // The cursor's epic entry, which the epic panel draws beside the conversation. A step entry is no epic entry: its reader
  // shows alone.
  const epicEntry = cursorItem?.kind === "epic" ? cursorItem : null;
  // The epic's steerer: the row of the session that steers it, as the rail names it. The middle row is the row the
  // reader column draws: the cursor's row, or for an epic entry its steerer's row. The entry the reader follows is the
  // middle row, or else the cursor's step. Its stream is kept under this key, so a stream never shows under another entry.
  const listedSteering = epicEntry === null ? "" : (epics.listed.get(epicEntry.id)?.steering ?? "");
  const steering =
    steered !== null && epicEntry !== null && steered.epic === epicEntry.id && steered.listed === listedSteering
      ? steered.session
      : listedSteering;
  const steererRow = epicEntry === null ? null : steererRowOf(rows, steering);
  const middleRow = cursorRow ?? steererRow;
  const middleKey = middleRow?.key ?? "";
  const readerKey = middleKey !== "" ? middleKey : cursorStepKey;
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
  // The epic entry the cursor rests on, when the poll did not load its detail, is asked for once with Epics.Get. A
  // failed answer leaves the page at its unavailable line.
  const askedId = epicEntry?.id ?? "";
  const polledDetail = askedId !== "" && epics.details.has(askedId);
  // The address the page shows. The epic page's reset to the open page is a layout effect of the render that moves the
  // cursor. So a render that moves the cursor reads the open page, and the page the cursor left is shown only once that
  // reset has run.
  const shownPage = pageCursor.current === cursorKey ? epicPage : openEpicPage;
  // The list panel the cursor's session row shows: a list is open only while the cursor rests on the row it is open on.
  const listShown: ListPanelKind | null = listPanel !== null && listPanel.key === cursorRowKey ? listPanel.panel : null;
  const shownAddress: Address = { order: view, entry: shownEntry(cursorItem, shownPage, listShown) };
  const shownForm = formatAddress(shownAddress);

  // The held cursor follows the entry shown, so a first render or a clamp does not leave it on a stale key.
  useEffect(() => {
    if (cursor.key !== cursorKey || cursor.index !== shownIndex) {
      setCursor({ key: cursorKey, index: shownIndex });
    }
  }, [cursor, cursorKey, shownIndex]);

  // The middle row, or the cursor's step, is followed while the cursor rests on it: a move to another entry, or an unmount,
  // stops the stream. A step is followed as its session, by its id.
  useEffect(() => {
    let target: Followed | null = null;
    if (middleRow !== null) {
      target = { kind: middleRow.kind, id: middleRow.id };
    } else if (cursorStep !== null) {
      target = { kind: "session", id: cursorStep.id };
    }
    if (target === null) {
      return undefined;
    }
    const followed = follow(clients, target, (conversation) => {
      setStream({ key: readerKey, conversation });
      setInterrupts((previous) => withoutTakenOver(previous, readerKey, conversation));
    });
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
    setVisited((previous) => keepEntries(previous, rows, middleRow));
  }, [rows, middleKey]);

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
    const followed = follow(clients, { kind: pinned.kind, id: pinned.id }, (conversation) => {
      setPinStream({ key: pinned.key, conversation });
      setInterrupts((previous) => withoutTakenOver(previous, pinned.key, conversation));
    });
    pinnedOlder.current = followed.older;
    return followed.stop;
  }, [pinnedKey, clients]);

  // A pinned row keeps its last shape while it is missing from the list, as a visited row does.
  useEffect(() => {
    setPinned((previous) => (previous === null ? null : (rows.find((row) => row.key === previous.key) ?? previous)));
  }, [rows]);

  // An interrupt mark clears once its row is in the list and no longer working. A row missing from the list keeps its
  // mark, as a pinned row keeps its last shape.
  useEffect(() => {
    setInterrupts((previous) => {
      const kept = new Map(
        [...previous].filter(([key]) => {
          const row = rows.find((item) => item.key === key);
          return row === undefined || row.state === stateWorking;
        }),
      );
      return kept.size === previous.size ? previous : kept;
    });
  }, [rows]);

  // A find bar closes when its column's row changes: the cursor moves to another entry, or the pin closes or moves.
  useEffect(() => {
    setCursorFind((previous) => (previous !== null && previous.key !== middleKey ? null : previous));
  }, [middleKey]);
  useEffect(() => {
    setPinnedFind((previous) => (previous !== null && previous.key !== pinnedKey ? null : previous));
  }, [pinnedKey]);

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
    pageCursor.current = cursorKey;
    setEpicPage(openEpicPage);
  }, [cursorKey]);

  // A move of the cursor shuts a list panel, unless the cursor moved onto the row the panel is open on.
  useLayoutEffect(() => {
    setListPanel((previous) => (previous?.key === cursorKey ? previous : null));
  }, [cursorKey]);

  // onViewing reports the viewing key of the cursor's entry each time that entry's kind or id changes, so the page's
  // presence reports name what it shows.
  useEffect(() => {
    onViewing?.(viewingOf(cursorItem));
  }, [cursorItem?.kind, cursorItem?.id]);

  // A back restores the scroll place of the page it returns to before that page is painted.
  useLayoutEffect(() => {
    if (restorePageScroll.current !== null && pageRef.current !== null) {
      pageRef.current.scrollTop = restorePageScroll.current;
    }
    restorePageScroll.current = null;
  }, [epicPage]);

  // An opening focuses the reply box it names, with the caret at the end of its text. The box is drawn by the same render
  // that moved the cursor or pinned the row, so it is in its ref here.
  useLayoutEffect(() => {
    if (boxOpened === null) {
      return;
    }
    const box = (boxOpened.column === "pinned" ? pinnedInputs : composerInputs).current.get(boxOpened.key);
    if (box !== undefined) {
      box.focus();
      box.setSelectionRange(box.value.length, box.value.length);
    }
    setBoxOpened(null);
  }, [boxOpened]);

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
  // with the kind the entry names and the id it archives by: a row's archiveId, which for a pane is its orrery_id, and
  // an epic's id. A pane with no orrery_id has no archive kind, so it does nothing. A landed call marks the entry as the
  // call left it, keyed by the entry's key, until the next list answer; a failed call leaves the entry where it was.
  const setArchive = (entry: Row | EpicItem, archived: boolean) => {
    const kind = entry.kind === "epic" ? "epic" : entry.archiveKind;
    if (kind === "none") {
      return;
    }
    const request = { kind, id: entry.kind === "epic" ? entry.id : entry.archiveId };
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

  // archiveFromHead archives the row a page's More menu names, through Epics.Archive with the row's archive kind and id. A
  // landed call marks the row archived, as the list's archive does. A failed one puts its reason under the title row of
  // that row, and changes nothing.
  const archiveFromHead = (row: Row) => {
    if (row.archiveKind === "none") {
      return;
    }
    void clients.epics.archive({ kind: row.archiveKind, id: row.archiveId }).then(
      () => {
        setRowMarks((previous) => new Map(previous).set(row.key, true));
        setHeadNote((previous) => (previous.key === row.key ? noHeadNote : previous));
      },
      (error: unknown) =>
        setHeadNote({ key: row.key, text: `Could not archive this session: ${failureReason(error)}. Nothing changed.` }),
    );
  };

  // A menu whose entry leaves the list closes, so it does not reopen when the entry returns. While a menu is open, a
  // pointerdown anywhere closes it: the ⋯ and the menu stop their own pointerdowns, so only a press outside reaches here.
  useEffect(() => {
    if (menuKey !== null && !menuDrawn) {
      setMenuKey(null);
    }
  }, [menuKey, menuDrawn]);
  useEffect(() => {
    if (menuKey === null) {
      return;
    }
    const close = () => setMenuKey(null);
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [menuKey]);

  // archiveControl is the ⋯ menu an entry's line draws. Its one item is Archive in a default view, and Unarchive in the
  // Archived one. toggle opens the entry's menu, or shuts it when it is the open one, so only one menu is open.
  const archiveControl = (entry: Row | EpicItem): ArchiveControl => ({
    archived: view === "archived",
    open: openMenu === entry.key,
    run: () => setArchive(entry, view !== "archived"),
    toggle: () => setMenuKey((current) => (current === entry.key ? null : entry.key)),
    close: () => setMenuKey(null),
  });

  // answerCursor sends the choice to the middle row's waiting question through actions.answer, with the conversation
  // the reader holds for that row only. A false result or a rejection shows a fixed line; nothing is retried. A landed
  // answer names its choice on the row, when the question offers that choice. While an epic is open in the panel its
  // question's cards take the digits, so none answers the steering conversation beside it.
  const answerCursor = (choice: 1 | 2 | 3) => {
    if (middleRow === null || (epicEntry !== null && !epicPage.closed)) {
      return;
    }
    const row = middleRow;
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
  // The conversations the readers draw, each without the prompts its queue still holds or has discarded, and the worker
  // session's, shown the same way.
  const cursorShown = useMemo(() => shownConversation(cursorConversation), [cursorConversation]);
  const pinnedShown = useMemo(() => shownConversation(pinnedConversation), [pinnedConversation]);
  const sessionShown = useMemo(() => shownConversation(sessionStream?.conversation ?? null), [sessionStream]);
  // A column's find bar shows only while its row is the column's. Its matches are the query's over the column's
  // conversation, and its current match is clamped to them where it is read.
  const cursorFinding = cursorFind !== null && cursorFind.key === middleKey ? cursorFind : null;
  const cursorMatches = useMemo(
    () => findMatches(cursorShown?.items ?? [], cursorFinding?.query ?? ""),
    [cursorShown, cursorFinding?.query],
  );
  const cursorCurrent = cursorFinding === null ? -1 : findCurrent(cursorFinding.current, cursorMatches.length);
  const pinnedFinding = pinnedFind !== null && pinnedFind.key === pinnedKey ? pinnedFind : null;
  const pinnedMatches = useMemo(
    () => findMatches(pinnedShown?.items ?? [], pinnedFinding?.query ?? ""),
    [pinnedShown, pinnedFinding?.query],
  );
  const pinnedCurrent = pinnedFinding === null ? -1 : findCurrent(pinnedFinding.current, pinnedMatches.length);
  // The messages each reader shows as sending: its row's entries the conversation it holds has not sent yet.
  const cursorSending =
    middleRow === null ? [] : unsentEntries(sendingByRow.get(middleRow.key) ?? [], cursorConversation, middleRow.kind);
  const pinnedSending =
    pinned === null ? [] : unsentEntries(sendingByRow.get(pinned.key) ?? [], pinnedConversation, pinned.kind);
  // A stored prompt that a reader's stream holds ends its entry, so the entry leaves the page.
  useEffect(() => {
    setSendingByRow((previous) =>
      pruneSent(previous, [
        { key: middleKey, kind: middleRow?.kind ?? "session", conversation: cursorConversation },
        { key: pinnedKey, kind: pinned?.kind ?? "session", conversation: pinnedConversation },
      ]),
    );
  }, [cursorConversation, pinnedConversation, middleKey, pinnedKey]);
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
  // dropTaken removes the texts an Escape took back from the row: its sending entries, and the local queued entry each of
  // its composers holds. The row's session queue is the server's own, and a stream's next delta replaces it.
  const dropTaken = (key: string, texts: string[]) => {
    setSendingByRow((previous) => withoutSent(previous, key, texts));
    composerHandles.current.get(key)?.forget(texts);
    pinnedHandles.current.get(key)?.forget(texts);
  };
  // takeBack puts texts an Escape took back into the target's composer after the text it holds, and drops the row's
  // sending and local entries for them. No texts changes nothing.
  const takeBack = ({ row, column }: EscapeTarget, texts: string[]) => {
    if (texts.length > 0) {
      dropTaken(row.key, texts);
      (column === "pinned" ? pinnedHandles : composerHandles).current.get(row.key)?.append(texts);
    }
  };
  // markAtPress sets a row's interrupt mark at the press, holding the turn_since its stream holds: the cursor's or the
  // pin's, whichever streams the row, and 0 when neither does.
  const markAtPress = (key: string) => {
    const conversation = key === cursorRowKey ? cursorConversation : key === pinnedKey ? pinnedConversation : null;
    const turn = Number(conversation?.state?.turnSince ?? 0n);
    setInterrupts((previous) => new Map(previous).set(key, turn));
  };
  // escapeRow takes back a row's queued messages and interrupts it when it was working at the press. A pane pulls its
  // queued input back, then gets the Escape key. A session takes back nothing by itself: its interrupt answers the
  // texts of the queued messages Claude cancelled, and those are taken back. A session that is not working is not
  // interrupted and takes nothing back. A headless session's interrupt is marked on the page at once, before the calls
  // below are made. A failed take back shows its line, and a pane's interrupt still goes out. A failed interrupt shows
  // its line and clears the mark. The composer keeps focus when it interrupted or took messages back, and otherwise
  // leaves it, as Escape does.
  const escapeRow = async (target: EscapeTarget, box: HTMLElement | null): Promise<void> => {
    const { row, state } = target;
    const interrupting = state === stateWorking;
    const marked = interrupting && row.kind === "session";
    const failures: string[] = [];
    if (marked) {
      markAtPress(row.key);
    }
    let texts: string[] = [];
    if (row.kind === "pane") {
      try {
        texts = await actions.withdraw(clients, row);
      } catch {
        failures.push(takeBackFailedText);
      }
      takeBack(target, texts);
    }
    if (interrupting) {
      let cancelled: string[] = [];
      try {
        cancelled = await actions.interrupt(clients, row);
      } catch {
        failures.push(interruptFailedText);
        if (marked) {
          setInterrupts((previous) => withoutKey(previous, row.key));
        }
      }
      takeBack(target, cancelled);
    }
    setEscapeError(failures.join(" "));
    if (box !== null && !interrupting && texts.length === 0) {
      box.blur();
    }
  };
  // interruptRow is Send now's interrupt on a session row: the row's interrupt mark is set at once, as Escape sets it, and
  // cleared when the interrupt is refused. It resolves the texts the interrupt cancelled.
  const interruptRow = async (row: Row): Promise<string[]> => {
    markAtPress(row.key);
    try {
      return await actions.interrupt(clients, row);
    } catch (failure) {
      setInterrupts((previous) => withoutKey(previous, row.key));
      throw failure;
    }
  };
  const toggleOpen = () => setOpen((value) => !value);
  const toggleStrips = () => setStripsShown((value) => !value);
  // hideHeader hides the phone header until it is opened, and the setting is kept on the device. showHeader opens it.
  const hideHeader = () => {
    keptHead.setHidden(true);
    setHeadHidden(true);
  };
  const showHeader = () => {
    keptHead.setHidden(false);
    setHeadHidden(false);
  };
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
  const back = navigated(() => leaveTo(epicPage.below.length - 1));
  const toEpicPage = navigated(() => leaveTo(0));
  // backOneCrumb is h and the left arrow. On a contract, phase output or worker session page it does that page's back.
  // On the epic page, a row's page or a step's page, which is at its first crumb, it pops a hidden inbox out by key.
  const backOneCrumb = () => {
    if (epicEntry !== null && epicPage.page.kind !== "epic") {
      back();
    } else if (inboxHidden) {
      setPopout("key");
    }
  };
  // openPhase opens a task's phase output over the epic page. On the phase output, a head opens its phase in place.
  const openPhase = navigated((task: string, phase: Phase) => {
    if (epicPage.page.kind === "phase") {
      setEpicPage((previous) => ({ ...previous, page: { kind: "phase", task, phase } }));
      return;
    }
    drill({ kind: "phase", task, phase });
  });
  const openContract = navigated(() => drill({ kind: "contract" }));
  const closeEpicPage = navigated(() => setEpicPage(closedEpicPage));

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

  // A step a card opened lands the epic page on the step's worker session, over the epic page, as the epic page's step
  // link does. It is declared after the cursor's reset above, so the commit where the cursor moves to the epic leaves the
  // session page, not the epic page the reset sets. It waits until the epic's detail holds the step.
  useLayoutEffect(() => {
    if (steppedTo === null) {
      return;
    }
    const detail = epics.details.get(steppedTo.epic) ?? askedDetails.get(steppedTo.epic);
    const step = detail?.steps.find((candidate) => candidate.id === steppedTo.step);
    if (detail === undefined || step === undefined) {
      return;
    }
    setSteppedTo(null);
    setEpicPage({
      page: { kind: "session", task: step.task, phase: phaseOfStep(detail, step), step: step.id },
      below: [{ page: epicPageView, scroll: 0 }],
      move: "drill",
      closed: false,
    });
  }, [steppedTo, epics, askedDetails]);

  // An opened address's epic sub-page lands the epic page on the page the address names, over the epic page for a
  // contract, phase output or worker session. It is declared after the cursor's reset above, so in the commit where the
  // address moves the cursor the sub-page is the page the reset leaves.
  // Once the page is set, the effect clears opened, so the render after it is the one whose report settles the address.
  useLayoutEffect(() => {
    if (opened === null) {
      return;
    }
    const { page } = opened.entry;
    setEpicPage(
      page.kind === "epic"
        ? openEpicPage
        : { page, below: [{ page: epicPageView, scroll: 0 }], move: "", closed: false },
    );
    setOpened(null);
  }, [opened]);

  const moveTo = (target: number) => {
    if (flat.length === 0) {
      return;
    }
    const next = clamp(target, flat.length);
    setCursor({ key: flat[next].key, index: next });
  };

  // selectItem moves the cursor to an entry the list shows, as a click on that entry does. It is an open.
  const selectItem = navigated((item: Item) => {
    moveTo(flat.findIndex((candidate) => candidate.key === item.key));
  });

  // openView sets the order, as a view tab or the a, p and v keys do. It is an open.
  const openView = navigated(setView);

  // App reports its address after each render, once, when the form it shows differs from the one it last reported or
  // applied. An address still pending, or an epic sub-page still landing, reports nothing and leaves its cause to the
  // render that settles it. This effect comes before the effects that set a cause or apply an address, so a cause they
  // set reaches the next render's report, and an address they apply does not reach this one.
  useEffect(() => {
    if (pendingOpen.current !== null || opened !== null) {
      return;
    }
    const taken = cause.current;
    cause.current = null;
    const previous = lastForm.current;
    lastForm.current = shownForm;
    if (applied.current) {
      applied.current = false;
      return;
    }
    if (shownForm === previous || (taken === null && !engaged.current)) {
      return;
    }
    onAddress?.(shownAddress, taken === "push" ? "push" : "replace");
  });

  // The palette's answered row takes the cursor once the loaded rows hold it, so a row that arrives in a later render
  // is reached too. A filter that hides the row is accepted: the cursor then stays where it is. The answer is an open.
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
      navigated(moveTo)(target);
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

  // openEpicStep opens a step of an epic: the epic's entry, and the page of the step's worker session over the epic page.
  const openEpicStep = (epic: string, step: string) => {
    openEpic(epic);
    setSteppedTo({ epic, step });
  };

  // App opens the address its prop holds: on mount, and again when the prop's formatted form changes, unless that form
  // is the address App shows now. An open sets the view at once, and holds its entry in pendingOpen until the view
  // settles it, since the rows and epics can arrive after the mount. An address with no entry closes the epic page on a
  // later change only, since on mount the cursor's epic page is today's default.
  const addressForm = address === undefined ? "" : formatAddress(address);
  useEffect(() => {
    const first = !addressMounted.current;
    addressMounted.current = true;
    if (address === undefined) {
      return;
    }
    // A prop that already names the shown address applies nothing, so it arms nothing: an address App reported and the
    // prop holds again leaves the cursor keys' modes as they were.
    if (addressForm === shownForm) {
      pendingOpen.current = null;
      lastForm.current = shownForm;
      return;
    }
    setView(address.order);
    pendingOpen.current = address.entry === null ? null : { order: address.order, entry: address.entry };
    if (address.entry === null && !first && epicEntry !== null) {
      setEpicPage(closedEpicPage);
    }
    if (address.entry === null) {
      adopt();
    }
  }, [addressForm]);

  // The pending entry is decided in the first pass whose view is its address's order, so a pass still on the old view
  // decides nothing. Until the loaded list holds the entry, App keeps waiting: the rows and epics it is given, unfiltered
  // and unmarked, are the check start() makes. Once they hold it, the view either shows it or hides it. A shown entry
  // opens: a row or step moves the cursor to its key and opens the row, as a click does, with no seen write and no reply
  // box, and an epic opens the line that holds it and moves the cursor to it, with its sub-page landing in the layout
  // effect above. A hidden one, by the filter or a local archive mark, is dropped: the cursor stays where it is, and App
  // reports its shown address with replace, arming the next cursor key as an applied address does. This effect is
  // declared after the held cursor's effect, so its cursor move wins over that effect in the commit it applies.
  useEffect(() => {
    const pending = pendingOpen.current;
    if (pending === null || pending.order !== view) {
      return;
    }
    if (!listHolds(pending, rows, epics)) {
      return;
    }
    const { entry } = pending;
    pendingOpen.current = null;
    if (entry.kind === "epic") {
      if (listHolds(pending, rows, marked)) {
        openEpic(entry.id);
        setOpened({ entry });
        setRowOpened(true);
        adopt();
        return;
      }
    } else {
      const item = flat.find((candidate) => candidate.key === addressKey(entry));
      if (item !== undefined) {
        selectItem(item);
        setListPanel(
          entry.kind === "session" && entry.panel !== undefined ? { key: item.key, panel: entry.panel } : null,
        );
        setRowOpened(true);
        adopt();
        return;
      }
    }
    // lastForm is cleared so the render that follows reports the shown address, even when it equals the last form App
    // reported.
    arm();
    lastForm.current = null;
    renderAddress();
  }, [addressForm, flat, marked]);

  // markNoticeSeen sends the notice's key as seen. A failed report is dropped; the notice stays until it ends.
  const markNoticeSeen = (notice: InAppNotice) => {
    void clients.notices.seen({ key: notice.key }).catch(() => undefined);
  };

  // openNotice counts a notice seen, then lands on what it names. An epic moves the cursor to its entry, wherever the
  // list holds it, and lands its page on the block the notice names. A pane or a session moves the cursor to its row or
  // step, when the list shows it. A failed report is dropped; the notice stays in the banner until it ends.
  const openNotice = navigated((notice: InAppNotice) => {
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
  });

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

  // togglePin pins the inbox beside the page, or unpins it: the mark's click and B both do this.
  const togglePin = () => setInboxPinned((value) => !value);
  // inboxMark is the mark every title row draws: its count, the hover that pops the inbox out, and the click that pins
  // it. A pointer that leaves the mark for the inbox keeps the inbox out; any other leave tucks it away. A hover does
  // not change a pop-out a key already made, and a narrow window takes no hover at all.
  const inboxMark: InboxMark = {
    needs: markNeeds,
    enter: () => {
      if (wide) {
        setPopout((current) => (current === "none" ? "hover" : current));
      }
    },
    leave: (to) => {
      if (!(to instanceof Node) || listRef.current?.contains(to) !== true) {
        setPopout("none");
      }
    },
    pin: togglePin,
  };

  // pageStrip is the strip a page draws under its title row: the host part stripHost names for the row the page shows,
  // and the context of a pane row's conversation, which the stream holds. A page with no row names the serving host. m
  // hides every strip, and then a page draws none.
  const pageStrip = (row: Row | null, conversation: Conversation | null) =>
    stripsShown ? (
      <PaneStrip
        host={stripHost(row, host)}
        context={row?.kind === "pane" ? conversation?.state?.context : undefined}
      />
    ) : undefined;

  // headOf is what a row's head draws: the strip's host, the asks its conversation draws, the note the More menu's archive
  // left for it, and the More menu's items for it. The asks are the epics steered by the row's id, and the row's own
  // question asks while the row waits and its pane or session is not put off. Under 760px the middle conversation's head
  // also draws its groups as tiles, since the desk's rail that draws them is not drawn; only the middle column's head
  // does, since the tiles act on the middle conversation.
  const headOf = (row: Row, conversation: Conversation | null, middle = false): ColumnHead => {
    const steering = steeredEpics(row.archiveId, marked.listed.values());
    const lines = askLines(steering, epics.details);
    const ownName = ownAsks(row, conversation) ? lastPathElement(row.project) || row.label : null;
    return {
      host: stripHost(row, host),
      showStrip: stripsShown,
      lines,
      askingWords: askingWords(ownName, lines),
      badges: headBadges(steering, lines, conversation?.state?.background ?? []),
      here: middle && panelOpen && epicEntry !== null ? epicEntry.id : undefined,
      work:
        middle && !wide
          ? { groups: workOf(workConversation(row, conversation), kept, now), lit, onOpen: openThing }
          : undefined,
      note: headNote.key === row.key ? headNote.text : "",
      menu: {
        stripsShown,
        toggleStrips,
        markSeen: () => void writeSeen(row),
        archive: row.archived || row.archiveKind === "none" ? null : () => archiveFromHead(row),
        keys: () => setHelpOpen((open) => !open),
      },
    };
  };

  // The note under a row's title row is cleared once the row it names leaves both reader columns.
  useEffect(() => {
    setHeadNote((previous) => (previous.key === cursorRowKey || previous.key === pinnedKey ? previous : noHeadNote));
  }, [cursorRowKey, pinnedKey]);

  // focusCursorRow moves focus to the list's current row, the row the cursor rests on. A hidden inbox holds that row in
  // the list the page hides, so it is popped out by key first, and the focus waits for the layout effect after the
  // render that draws it, as a browser takes no focus on a row it does not display.
  const focusPending = useRef(false);
  useLayoutEffect(() => {
    if (focusPending.current) {
      focusPending.current = false;
      document.querySelector<HTMLElement>(".b-row.cur")?.focus();
    }
  });
  const focusCursorRow = () => {
    if (inboxHidden) {
      focusPending.current = true;
      setPopout("key");
      return;
    }
    document.querySelector<HTMLElement>(".b-row.cur")?.focus();
  };

  // replyBoxTarget is the row whose reply box is focused, when it is the cursor's or the pinned row's, with the row's
  // state at the press. A step's box and any other box are none.
  const replyBoxTarget = (box: HTMLElement): EscapeTarget | null => {
    if (middleRow !== null && composerInputs.current.get(middleRow.key) === box) {
      return { row: middleRow, column: "cursor", state: headState(middleRow, cursorConversation) };
    }
    if (pinned !== null && pinnedInputs.current.get(pinned.key) === box) {
      return { row: pinned, column: "pinned", state: headState(pinned, pinnedConversation) };
    }
    return null;
  };

  // The rail's conversation is the middle row, with its session, whether it is a pane, and the background tasks its stream
  // holds. Its groups are what it steers, from the epics the list shows.
  const railConversation: WorkConversation | null =
    middleRow === null ? null : workConversation(middleRow, cursorConversation);
  const railGroups = workOf(railConversation, kept, now);
  // The epic panel is open while the cursor rests on an epic entry and its page is not closed. Its tile is the rail's tile
  // of that epic, which is lit while the panel is open, and whose tone the card takes. An epic no conversation steers has
  // no tile, so no tile is lit and the card takes the quiet tone.
  const panelOpen = epicEntry !== null && !epicPage.closed;
  // Under 760px an open epic is the page in the conversation's place: the conversation is not drawn, and the page's
  // foot bar opens the steering conversation. At the desk the epic panel lies beside the conversation.
  const epicPageShown = !wide && panelOpen;
  const panelThing =
    epicEntry === null
      ? undefined
      : railGroups.flatMap((group) => group.things).find((thing) => thing.key === epicEntry.key);
  const lit = panelOpen && panelThing !== undefined ? panelThing.key : (listShown ?? "");
  const panelTone = panelThing?.tone ?? "quiet";
  // The one panel open beside the conversation is an epic's or a list's. A list's card takes its tile's tone, and its
  // rows are the tile's tasks, which are empty once the tile is gone.
  const panelShown = panelOpen || listShown !== null;
  const listThing = railGroups
    .flatMap((group) => group.things)
    .find((thing): thing is ListThing => thing.kind !== "epic" && thing.key === listShown);
  const listTone = listThing?.tone ?? "quiet";
  const listTasks = listThing?.tasks ?? [];
  // What the panel holds while it is open: ] reopens it once it shuts. An epic is held while its panel is open, and a
  // list while its panel is open on its row.
  const openHeld: Held | null =
    panelOpen && epicEntry !== null
      ? { kind: "epic", id: epicEntry.id }
      : listShown === null
        ? null
        : { kind: "list", open: { key: cursorRowKey, panel: listShown } };
  const heldKey =
    openHeld === null
      ? ""
      : openHeld.kind === "epic"
        ? `epic:${openHeld.id}`
        : `list:${openHeld.open.key}:${openHeld.open.panel}`;
  useEffect(() => {
    if (openHeld !== null) {
      setHeld(openHeld);
    }
  }, [heldKey]);
  // closePanel shuts the epic panel, from its lit tile or its Close. It moves the cursor to the steerer's row, as an open,
  // when the list shows that row; with no such row in the list, it closes the epic page instead.
  const closePanel = navigated(() => {
    const target = flat.findIndex((item) => item.key === middleKey);
    if (middleRow !== null && target >= 0) {
      moveTo(target);
    } else {
      setEpicPage(closedEpicPage);
    }
  });
  // openList opens a list panel on the session row it names, moving the cursor onto that row, which holds the panel
  // open. A row the list does not show opens nothing.
  const openList = (key: string, panel: ListPanelKind) => {
    const target = flat.findIndex((item) => item.key === key);
    if (target < 0) {
      return;
    }
    moveTo(target);
    setListPanel({ key, panel });
  };
  // closeList shuts the list panel, from its lit tile or its Close. The cursor stays on the session's row, so the
  // address is the session's own.
  const closeList = navigated(() => setListPanel(null));
  // openThing is a press on a rail tile. The lit tile shuts its panel, whether it is an epic's or a list's. Another
  // epic's tile opens that epic as an open, and a list's tile opens the list on the session row the rail reads.
  const openThing = (thing: Thing) => {
    if (thing.key === lit) {
      if (thing.kind === "epic") {
        closePanel();
      } else {
        closeList();
      }
      return;
    }
    if (thing.kind === "epic") {
      navigated(openEpic)(thing.id);
      return;
    }
    navigated(openList)(middleKey, thing.kind);
  };
  // togglePanel is ]: it shuts the open panel, or with none open reopens the thing the panel held last, as long as the
  // list still holds it.
  const togglePanel = () => {
    if (panelOpen) {
      closePanel();
      return;
    }
    if (listShown !== null) {
      closeList();
      return;
    }
    if (held === null) {
      return;
    }
    if (held.kind === "epic") {
      if (!listsEpic(kept, held.id)) {
        return;
      }
      // An epic shut with no conversation to go to keeps the cursor, so its page is opened again where it is.
      if (cursorKey === `epic:${held.id}`) {
        navigated(() => setEpicPage(openEpicPage))();
      } else {
        navigated(openEpic)(held.id);
      }
      return;
    }
    navigated(openList)(held.open.key, held.open.panel);
  };

  // The keys read this render's state and call this render's actions, so the document listener runs the latest context.
  const keyContext: KeyContext = {
    pickerOpen,
    openMenu,
    helpOpen,
    inboxHidden,
    keyPopped,
    index,
    flat,
    cursorItem,
    cursorRow,
    cursorRowKey,
    cursorStep,
    cursorStepKey,
    cursorConversation,
    pinned,
    inputRef,
    composerInputs,
    stepInputs,
    pendingG,
    moveTo: keyMoved(moveTo),
    nextUnseen: keyMoved(nextUnseen),
    writeSeen,
    setArchive,
    escapeRow,
    replyBoxTarget,
    focusCursorRow,
    backOneCrumb: navigated(backOneCrumb),
    answerCursor,
    toggleOpen,
    closePin,
    togglePin,
    toggleStrips,
    togglePanel,
    setPickerOpen,
    setMenuKey,
    setFilter,
    setPopout,
    setRowOpened: navigated(setRowOpened),
    setBoxOpened,
    setView: openView,
    setPinned,
    setPinStream,
    setHelpOpen,
  };
  handler.current = (event: KeyboardEvent) => handleKey(event, keyContext);

  useEffect(() => {
    const listener = (event: KeyboardEvent) => handler.current?.(event);
    document.addEventListener("keydown", listener);
    return () => document.removeEventListener("keydown", listener);
  }, []);

  // The epic calls the readers of the cursor and the pin make: an epic a call created draws its card, which shows the
  // poll's detail of its epic, or reads an epic the poll holds no detail of when its listed message changes, and opens
  // the epic through openEpic; a step card opens its step through openEpicStep.
  const epicCalls = epicCallsOf(clients, navigated(openEpic), navigated(openEpicStep), epics);

  // epicPageSection draws the epic section over the epic's detail, or the line that the detail did not load.
  const epicPageSection = (entry: EpicItem, detail: EpicsGetResponse | undefined) => (
    <section class={pageClasses(epicPage.move)} aria-label="epic" ref={pageRef}>
      {detail === undefined ? <p class="side-note">{epicUnavailableText}</p> : pageNode(entry, detail)}
    </section>
  );

  // pageNode draws the page the section holds over the epic's detail: the epic page, or the contract, the phase output
  // or the worker session the page has moved to.
  const pageNode = (entry: EpicItem, detail: EpicsGetResponse) => {
    const { page } = epicPage;
    if (page.kind === "contract" && detail.contract !== undefined) {
      return (
        <div>
          <TitleRow
            crumbs={[{ label: detail.epic?.slug ?? "", onGo: toEpicPage }, { label: "contract" }]}
            onBack={back}
            mark={inboxMark}
            strip={pageStrip(null, null)}
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
          onOpenPhase={openPhase}
          onOpenSession={navigated((step: string) =>
            drill({ kind: "session", task: page.task, phase: page.phase, step }),
          )}
          hold={(step) => stepCalls(clients, step).hold()}
          stop={(step) => stepCalls(clients, step).stop()}
          mark={inboxMark}
          strip={pageStrip(null, null)}
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
          conversation={sessionStream !== null && sessionStream.key === page.step ? sessionShown : null}
          now={now}
          step={step}
          epic={detail.epic?.slug ?? ""}
          task={page.task}
          phase={page.phase}
          onGoEpic={toEpicPage}
          onBack={back}
          onOlder={() => sessionOlder.current()}
          nudge={calls.nudge}
          hold={calls.hold}
          stop={calls.stop}
          mark={inboxMark}
          strip={pageStrip(null, null)}
        />
      );
    }
    const openSession = (target: SessionTarget) => setPending({ kind: target.kind, id: target.id });
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
        onOpenSession={openSession}
        onOpenPhase={openPhase}
        onBack={closeEpicPage}
        handover={Handover({ detail })}
        mark={inboxMark}
        strip={pageStrip(null, null)}
        rows={rows}
        actions={
          <EpicSteer
            key={entry.id}
            epic={entry.id}
            slug={detail.epic?.slug ?? ""}
            repository={detail.repository}
            steering={steering}
            rows={rows}
            clients={clients}
            narrow={!wide}
            onSteered={(session) => setSteered({ epic: entry.id, listed: listedSteering, session })}
          />
        }
      />
    );
  };

  return (
    <main class="app">
      <div
        class={`panes${pinned !== null ? " split" : ""}${overlay ? " overlay" : ""}`}
        data-inbox={inboxState}
        data-side={panelShown ? "panel" : "rail"}
      >
        {notices.length > 0 && <NoticeBanner notices={notices} onOpen={openNotice} onDismiss={markNoticeSeen} />}
        <div class="list" ref={listRef} onMouseLeave={() => setPopout("none")}>
          <header class="top">
            <div class="brand">
              <Mark />
              orrery
              <button type="button" title="Start a session, or find one  N" onClick={() => setPickerOpen(true)}>
                <svg class="icon" viewBox="0 0 16 16" aria-hidden="true">
                  <path d="M8 3v10M3 8h10" />
                </svg>
                New
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
              <span class={view === "attention" ? "view on" : "view"} onClick={() => openView("attention")}>
                <kbd>a</kbd>By attention
              </span>
              <span class={view === "project" ? "view on" : "view"} onClick={() => openView("project")}>
                <kbd>p</kbd>By project
              </span>
              <span class={view === "archived" ? "view on" : "view"} onClick={() => openView("archived")}>
                <kbd>v</kbd>Archived
              </span>
            </div>
            <button type="button" class="keys-hint" aria-label="Keys" title="Keys  ?" onClick={() => setHelpOpen(true)}>
              <svg class="icon" viewBox="0 0 16 16" aria-hidden="true">
                <path d="M3.5 4h9A1.5 1.5 0 0 1 14 5.5v5a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 2 10.5v-5A1.5 1.5 0 0 1 3.5 4zM5 7h.5M7.8 7h.5M10.6 7h.5M5.5 9.5h5" />
              </svg>
            </button>
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
                  <InboxRow
                    key={item.key}
                    row={item}
                    now={now}
                    cursor={item.key === cursorRowKey}
                    archive={archiveControl(item)}
                    conversation={heldConversation(item.key)}
                    answer={answered.get(item.key)}
                    onOpen={() => {
                      selectItem(item);
                      setRowOpened(true);
                      setBoxOpened({ column: "cursor", key: item.key });
                    }}
                    onSeen={() => {
                      selectItem(item);
                      void writeSeen(item);
                    }}
                  />
                ))}
                {section.title === needsYouTitle &&
                  needs.map((need) => (
                    <NeedEntry
                      key={`${need.kind}:${need.epic}:${need.question}`}
                      need={need}
                      slug={epics.details.get(need.epic)?.epic?.slug ?? ""}
                      commands={epicCommands(clients, need.epic, setCommandError)}
                      onOpen={navigated(() => openEpic(need.epic))}
                    />
                  ))}
              </div>
            ))}
          </div>
        </div>
        <ReaderColumn
          column="cursor"
          clients={clients}
          now={now}
          row={epicPageShown ? null : middleRow}
          conversation={cursorConversation}
          rows={visited}
          interrupts={interrupts}
          mark={inboxMark}
          pageStrip={pageStrip}
          head={(row, conversation) => headOf(row, conversation, true)}
          narrow={!wide}
          hidden={headHidden}
          onHide={hideHeader}
          onShow={showHeader}
          onOpenEpic={navigated(openEpic)}
          onBack={navigated(focusCursorRow)}
          finding={cursorFinding}
          matches={cursorMatches}
          current={cursorCurrent}
          onFind={setCursorFind}
          inputs={composerInputs}
          handles={composerHandles}
          reader={cursorReader}
          older={cursorOlder}
          sending={cursorSending}
          epicCalls={epicCalls}
          addSending={addSending}
          retractSending={retractSending}
          open={open}
          onToggleOpen={toggleOpen}
          onAnswer={answerCursor}
          onInterrupt={(row) =>
            void escapeRow({ row, column: "cursor", state: headState(row, cursorConversation) }, null)
          }
          interrupt={interruptRow}
          step={cursorStep}
          steps={visitedSteps}
          stepInputs={stepInputs}
        />
        {epicEntry !== null && steererRow === null && !epicPageShown && (
          <section class="read" aria-label="no conversation">
            <p class="side-note">{noSteererText}</p>
          </section>
        )}
        <ReaderColumn
          column="pinned"
          clients={clients}
          now={now}
          row={pinned}
          conversation={pinnedConversation}
          rows={pinnedRows}
          interrupts={interrupts}
          mark={inboxMark}
          pageStrip={pageStrip}
          head={headOf}
          narrow={!wide}
          hidden={headHidden}
          onHide={hideHeader}
          onShow={showHeader}
          onOpenEpic={navigated(openEpic)}
          onBack={closePin}
          finding={pinnedFinding}
          matches={pinnedMatches}
          current={pinnedCurrent}
          onFind={setPinnedFind}
          inputs={pinnedInputs}
          handles={pinnedHandles}
          reader={pinnedReader}
          older={pinnedOlder}
          sending={pinnedSending}
          epicCalls={epicCalls}
          addSending={addSending}
          retractSending={retractSending}
          open={false}
          interrupt={interruptRow}
          onInterrupt={(row) =>
            void escapeRow({ row, column: "pinned", state: headState(row, pinnedConversation) }, null)
          }
        />
        {epicEntry !== null && panelOpen && wide && (
          <SidePanel label="epic" tone={panelTone} href={shownForm} onClose={closePanel}>
            {epicPageSection(epicEntry, epicDetail)}
          </SidePanel>
        )}
        {epicEntry !== null && panelOpen && !wide && (
          <div class="epic-read">
            {epicPageSection(epicEntry, epicDetail)}
            <div class="epic-foot">
              {steererRow === null ? (
                <p class="side-note">{noSteererText}</p>
              ) : (
                <button type="button" class="act" onClick={closePanel}>
                  Talk to the session that steers it
                </button>
              )}
            </div>
          </div>
        )}
        {listShown !== null && wide && (
          <SidePanel label={listHead(listShown)} tone={listTone} href={shownForm} onClose={closeList}>
            <ListPanel kind={listShown} tasks={listTasks} now={now} />
          </SidePanel>
        )}
        {listShown !== null && !wide && (
          <Sheet title={listHead(listShown)} onClose={closeList}>
            <ListPanel kind={listShown} tasks={listTasks} now={now} headless />
          </Sheet>
        )}
        {wide && <WorkRail groups={railGroups} lit={lit} layout="rail" onOpen={openThing} />}
      </div>
      {pickerOpen && (
        <NewPanel
          clients={clients}
          rows={rows}
          sheet={!wide}
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
      {helpOpen && <KeyList onClose={() => setHelpOpen(false)} />}
    </main>
  );
}

// keepsAnswer reports whether a landed answer stays on its row: the row is missing from the list, or it still waits on
// the question the answer named.
function keepsAnswer(rows: Row[], key: string, answer: Answer): boolean {
  const row = rows.find((candidate) => candidate.key === key);
  return row === undefined || (row.state === stateWaiting && row.question === answer.question);
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
    listed: view.listed,
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

// stripHost is the host a page's strip names. A pane row on another host names that host alone, with no stats. Any
// other page names the serving host with its stats, and with no status there is no host part.
function stripHost(row: Row | null, host: HostStatusResponse | null): StripHost | null {
  if (host === null) {
    return null;
  }
  if (row !== null && row.kind === "pane" && row.host !== "" && row.host !== host.host) {
    return { host: row.host, stats: [], other: true };
  }
  return host;
}

// failureReason is why a call failed, as a page's note says it: that the server did not answer, for a call that never
// reached it or failed unavailable, past its deadline or unknown, and the error's own message otherwise.
function failureReason(error: unknown): string {
  const failure = ConnectError.from(error);
  return noAnswerCodes.has(failure.code) ? noAnswerText : failure.rawMessage;
}

// withoutKey is a copy of marks without key.
function withoutKey(marks: Map<string, number>, key: string): Map<string, number> {
  const next = new Map(marks);
  next.delete(key);
  return next;
}

// withoutTakenOver drops key's interrupt mark once conversation, the stream the page holds for that row, takes over from
// it: the stream reports interrupting, or a turn other than the one the mark held at the press. A stream with no state
// yet keeps the mark, and marks comes back as it is when key holds none.
function withoutTakenOver(
  marks: Map<string, number>,
  key: string,
  conversation: Conversation | null,
): Map<string, number> {
  const held = marks.get(key);
  const state = conversation?.state;
  if (held === undefined || state === undefined || (!state.interrupting && Number(state.turnSince) === held)) {
    return marks;
  }
  return withoutKey(marks, key);
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

// phaseOfStep is the phase a step's session page names: the phase whose row opens the step, as the epic page's phase rows
// open it, or else reviewing for a read-only step and implementing for any other step.
function phaseOfStep(detail: EpicsGetResponse, step: EpicStep): Phase {
  const row = phaseOutputOf(detail, step.task)?.rows.find((candidate) => candidate.session === step.id);
  return row?.phase ?? (step.readOnly ? "reviewing" : "implementing");
}

// EpicSteer is the steering command in an epic page's title row. At the desk it is an icon that opens the steering menu
// under it. Under 760px it is a More whose one item opens the menu in a sheet. It holds the menu's open state and the
// one steering handle the menu and its choices share, and a choice that steers closes the menu and reports the session.
function EpicSteer({
  epic,
  slug,
  repository,
  steering,
  rows,
  clients,
  narrow,
  onSteered,
}: {
  epic: string;
  slug: string;
  repository: string;
  steering: string;
  rows: Row[];
  clients: Clients;
  narrow: boolean;
  onSteered: (session: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [sheet, setSheet] = useState(false);
  const steer = useSteer(epic, steerCallsOf(clients), (session) => {
    setOpen(false);
    setSheet(false);
    onSteered(session);
  });
  const menu = (onClose: () => void) => (
    <SteerMenu slug={slug} steering={steering} repository={repository} rows={rows} steer={steer} onClose={onClose} />
  );
  if (!narrow) {
    return (
      <span class="steer-anchor">
        <button
          type="button"
          class="icon steer"
          aria-label="Steer it from another conversation"
          title="Steer it from another conversation"
          aria-expanded={open}
          onClick={() => setOpen((isOpen) => !isOpen)}
        >
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path d="M3 5h9M10 2.5 12.5 5M13 11H4M6 8.5 3.5 11" />
          </svg>
        </button>
        {open && menu(() => setOpen(false))}
      </span>
    );
  }
  return (
    <>
      <button
        type="button"
        class="icon more"
        aria-label="More"
        title="Steer it from another conversation"
        aria-expanded={open}
        onClick={() => setOpen((isOpen) => !isOpen)}
      >
        <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
          <path d="M3.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM8 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2zM12.5 6.9a1.1 1.1 0 1 0 0 2.2 1.1 1.1 0 0 0 0-2.2z" />
        </svg>
      </button>
      {open && (
        <div class="head-menu" role="menu">
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              setSheet(true);
            }}
          >
            Steer it from another conversation
          </button>
        </div>
      )}
      {sheet && (
        <Sheet title={`Steer ${slug} from`} onClose={() => setSheet(false)}>
          {menu(() => setSheet(false))}
        </Sheet>
      )}
    </>
  );
}

// resolveIndex finds the cursor's entry, or the index it sat at, clamped into the entries; -1 when there are none.
function resolveIndex(items: Item[], cursor: Cursor): number {
  if (items.length === 0) {
    return -1;
  }
  const found = items.findIndex((item) => item.key === cursor.key);
  return found >= 0 ? found : clamp(cursor.index, items.length);
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

// workConversation is what a row's groups are drawn from: its session, whether it is a pane, and the background tasks
// its stream holds.
function workConversation(row: Row, conversation: Conversation | null): WorkConversation {
  return {
    session: row.archiveKind === "none" ? "" : row.archiveId,
    pane: row.kind === "pane",
    background: conversation?.state?.background ?? [],
  };
}

// shownEntry is the entry App's address names for the cursor's entry: a row or step by its id, a session by its id and
// the list panel open on it, and an epic by its id and its open sub-page. An epic whose page is closed has no entry,
// and neither has a cursor on nothing.
function shownEntry(item: Item | null, epicPage: EpicPageState, list: ListPanelKind | null): Entry | null {
  if (item === null) {
    return null;
  }
  switch (item.kind) {
    case "session":
      return list === null ? { kind: "session", id: item.id } : { kind: "session", id: item.id, panel: list };
    case "pane":
      return { kind: "pane", host: item.host, pane: item.id };
    case "step":
      return { kind: "step", id: item.id };
    case "epic":
      return epicPage.closed ? null : { kind: "epic", id: item.id, page: epicPage.page };
  }
}

// addressKey is the key the list gives the row or step an address names, which the cursor is moved to.
function addressKey(entry: Exclude<Entry, { kind: "epic" }>): string {
  switch (entry.kind) {
    case "session":
      return `session:${entry.id}`;
    case "pane":
      return `pane:${entry.host}:${entry.pane}`;
    case "step":
      return `step:${entry.id}`;
  }
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

function clamp(index: number, length: number): number {
  return Math.min(Math.max(index, 0), length - 1);
}

// findCurrent is the match a find bar shows: the stored one, clamped into the matches, and -1 when none match.
function findCurrent(stored: number, matches: number): number {
  return matches === 0 ? -1 : clamp(stored, matches);
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

// pageClasses are the classes of the epic page section: its move.
function pageClasses(move: EpicPageState["move"]): string {
  return ["page", move].filter((name) => name !== "").join(" ");
}

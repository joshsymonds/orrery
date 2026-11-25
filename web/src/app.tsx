import { toJsonString } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import type { JSX } from "preact";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks";

import { AccountsPage } from "./accountspage";
import * as actions from "./actions";
import type { Clients } from "./actions";
import { ActivityLines } from "./activitylines";
import { type AskTarget, conversationAsk } from "./askmodel";
import { askingWords, askLines, headBadges, ownAsks, steeredEpics } from "./asks";
import { type AskState, askKey, observe, press, settle } from "./askstate";
import { epicCallsOf } from "./card";
import type { DockHandle } from "./dock";
import { ContractBody, type ContractDesign } from "./contract";
import { ContractRecord } from "./contractrecord";
import { bandText, contractSlug, leftOf, type RowAbout, readKey, statePill } from "./contracts";
import { endedLead, EndedBar, unsteeredLead } from "./endedbar";
import { type Conversation, shownConversation } from "./conversation";
import { ConvoRow } from "./convorow";
import { DesignPage } from "./designpage";
import { designCountsOf, type DesignRecord, designRefetch } from "./designrecords";
import { type EpicCommands, EpicPage, type Landing, type SessionTarget, type SteerSheet } from "./epicpage";
import { epicGaugeOf, type GaugeShare } from "./epicrow";
import { type EpicItem, type EpicsView, epicsView, noEpics, type StepItem } from "./epics";
import { findMatches } from "./find";
import { type Followed, follow } from "./follow";
import type { Contract } from "./gen/orrery/v1/contracts_pb";
import { type Design, DesignSchema } from "./gen/orrery/v1/designs_pb";
import type { Epic, EpicHistoryEntry, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import type { InAppNotice, NoticeKey } from "./gen/orrery/v1/notice_pb";
import type { HostStatusResponse } from "./gen/orrery/v1/orrery_pb";
import { Icon, stateWord } from "./glyphs";
import { type HeadStore, memoryHeadStore } from "./headfold";
import { type HeldMove, type HeldThing, heldMove } from "./held";
import { HeldCard, HeldSheet } from "./heldcard";
import { KeyList } from "./helppanel";
import { Inbox } from "./inbox";
import { InboxRail } from "./inboxrail";
import { ageText, InboxRow, type RowMore } from "./inboxrow";
import {
  type BoxOpen,
  type EscapeTarget,
  handleKey,
  isRow,
  type Item,
  type KeyContext,
} from "./keymap";
import { listHolds, listsEpic } from "./listholds";
import { type Move, moveNotice, movesBetween, type Placed, spotsOf } from "./moves";
import { NoticeCentre } from "./noticecentre";
import { useNoticeLog } from "./noticelog";
import { NoticeStack } from "./notices";
import { bringBackRowOf, type NoticeFields, noticeAct, noticeAge, noticeHead, noticeKey } from "./noticewords";
import { sendOnce } from "./ownercall";
import { briefOf, briefStepOf, PhaseOutputPage } from "./phaseoutput";
import type { Phase } from "./phases";
import { NewPanel } from "./picker";
import { type Viewing, viewingOf } from "./presence";
import { ProjectPage } from "./projectpage";
import type { QuestionAnswer } from "./questionthread";
import { type ContractCalls, type DesignCalls, newestItemKey, type ReaderHandle, withDesignCalls } from "./reader";
import { flyoutOf, type FlyoutRow, railTiles } from "./railtiles";
import { type Address, type Entry, formatAddress, type ListPanelKind, type PageView, type View } from "./router";
import type { Row } from "./rows";
import { realClock, type SeenClock, type SeenWindow, watchSeen } from "./seen";
import { type SendKind, type Sending, type SentText, sendingEntry, unsentEntries, withoutTexts } from "./sending";
import {
  type AskCalls,
  type AskNote,
  type ColumnHead,
  type FindState,
  headState,
  ReaderColumn,
  stepCalls,
} from "./readercolumn";
import { Sheet } from "./sheet";
import type { ContractBandLine } from "./subjectband";
import { ListPanel, listHead, SidePanel } from "./sidepanel";
import { conversationEnded, steerCallsOf, steererRowOf, steersLive } from "./steer";
import { SteerMenu, useSteer } from "./steermenu";
import { PaneStrip, type StripHost } from "./strip";
import { lastPathElement } from "./titleparts";
import { TitleRow } from "./titlerow";
import { Toast } from "./toast";
import { buildTree, type Convo, type Hung, type Project } from "./tree";
import { countsOf, type Narrow, narrow as narrowTree, projectWaiting, search as searchTree, waitOrder } from "./treefilter";
import { activityOf, type ListThing, type WorkConversation, type WorkLinkItem, workLinksOf, workOf } from "./work";
import { WorkerSessionPage } from "./workersession";

// A project's flyout beside the collapsed inbox's rail: the project's key, and whether the pointer or a key opened it. The
// pointer leaving the rail closes only a flyout the pointer opened.
interface Flyout {
  key: string;
  by: "hover" | "key";
}

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

// The contract an opened address names, with the cursor key it opened over: its panel is open while the cursor rests
// there.
interface OpenedContract {
  id: string;
  cursor: string;
}

// The design a press on its tab, its title link or its rail mark opened, with the cursor key it opened over: its card is
// held while the cursor rests there.
interface OpenedDesign {
  id: string;
  cursor: string;
}

// A pending open is the entry an opened address names, with the view it names. The entry is decided only in a pass whose
// view is that view.
interface PendingOpen {
  view: View;
  entry: Entry;
}

// A list panel open beside a session: the key of the session's row it is open on, and the list it shows.
interface ListOpen {
  key: string;
  panel: ListPanelKind;
}

// What a panel held, which ] reopens once no panel is open: a design, an epic or a contract by its id, or a session's list by
// its open state.
type Held =
  | { kind: "design"; id: string }
  | { kind: "epic"; id: string }
  | { kind: "contract"; id: string }
  | { kind: "list"; open: ListOpen };

// The row the palette answered: the pane Picker.Run answered, or the session Control.Start started, by its kind and id.
interface PendingTarget {
  kind: Row["kind"];
  id: string;
}

// The toast's notice: the row an archive under 760px just archived, which Undo unarchives, or the moves a list answer
// made, which Show and Undo act on.
type ToastNotice = { kind: "archived"; row: Row } | { kind: "moved"; moves: Move[] };

// The note a page's More menu left under a row's title row, which names the row it is for.
interface HeadNote {
  key: string;
  text: string;
}

const noHeadNote: HeadNote = { key: "", text: "" };

const stateWorking = "working";
// The block kind a notice names when it opens the contract page.
const contractBlock = "contract";
const loadingEpicText = "loading the epic…";
const couldNotReadEpicText = "The server could not read this epic.";
const noSteererText = "No conversation steers this epic.";
const noProjectText = "Nothing in the inbox is in this project.";
// The session states that end a conversation: its row holds it, but it is not live.
const endedStates: readonly string[] = ["stopped", "failed"];
const seenFailedText = "could not mark seen";
const answerFailedText = "no answer was sent";
// noAskNote is the Ask note of no call: no key, and no reason.
const noAskNote: AskNote = { key: "", text: "" };
const takeBackFailedText = "could not take the queued messages back";
const interruptFailedText = "could not interrupt";
// The reason a failed command shows when its error carries no message.
const noReasonText = "no reason was given";
const sessionUnavailableText = "session unavailable";
const archiveFailedText = "could not change archive";
const noAnswerText = "the server did not answer";
// The codes a call's failure reads as the server not answering.
const noAnswerCodes = new Set([Code.Unavailable, Code.DeadlineExceeded, Code.Unknown]);
// How long the toast stays, in milliseconds on the App's clock, when neither of its actions is pressed first.
const toastMs = 5000;
// The most entries one page of an epic's history asks for.
const historyPageSize = 200;

// The query of the wide layout, the same one style.css's wide block uses. Only that layout collapses the inbox to its
// rail: a narrower window always draws it as the stacked layout does.
const wideQuery = "(min-width: 760px)";

// The query of the layout at 1250px and under, where the epic panel lies over the conversation from the right, beside the
// rail, rather than taking a track of its own.
const overlayQuery = "(max-width: 1250px)";

// The query of reduced motion, the one tokens.css's reduced block uses. While it matches, a moved row is drawn with no
// mark, and still lands in its new place.
const reduceQuery = "(prefers-reduced-motion: reduce)";
// How long a moved row's mark stays, in milliseconds on the App's clock: the layout move's length in tokens.css.
const moveMs = 280;
// The contracts an App holds when none are given.
const noContracts: readonly Contract[] = [];
// The designs an App holds when none are given.
const noDesigns: readonly Design[] = [];
// The notices an App shows when none are given.
const noNotices: InAppNotice[] = [];

export function App({
  rows,
  answered = true,
  listFailed = false,
  onRetry,
  now,
  clients,
  seenClock,
  seenWindow,
  epics = noEpics,
  contracts = noContracts,
  designs = noDesigns,
  notices = noNotices,
  host = null,
  onViewing,
  address,
  onAddress,
  headStore,
}: {
  rows: Row[];
  // Whether the rows and the epics have each answered their first load. Until both have, the inbox says loading.
  answered?: boolean;
  // Whether the latest rows or epics load failed. While it has, the inbox says the server did not answer, in place of
  // the tree, and onRetry runs both loads again.
  listFailed?: boolean;
  onRetry?: () => void;
  now: number;
  clients: Clients;
  seenClock?: SeenClock;
  seenWindow?: SeenWindow;
  epics?: EpicsView;
  contracts?: readonly Contract[];
  designs?: readonly Design[];
  notices?: InAppNotice[];
  host?: HostStatusResponse | null;
  onViewing?: (viewing: Viewing) => void;
  address?: Address;
  onAddress?: (address: Address, mode: "push" | "replace") => void;
  headStore?: HeadStore;
}) {
  const [view, setView] = useState<View>("inbox");
  const [cursor, setCursor] = useState<Cursor>({ key: "", index: 0 });
  // The search text while the search field is open, and null while it is shut.
  const [searchText, setSearchText] = useState<string | null>(null);
  // The chip the tree is narrowed by.
  const [narrowBy, setNarrowBy] = useState<Narrow>("");
  // The focus the search field takes once the render that draws it has run.
  const focusSearch = useRef(false);
  const [helpOpen, setHelpOpen] = useState(false);
  // Whether the notice centre is open. Its opener sets it; the centre is drawn from it.
  const [noticesOpen, setNoticesOpen] = useState(false);
  const [open, setOpen] = useState(false);
  // Whether the strips under the title rows are drawn. m hides and shows every one of them.
  const [stripsShown, setStripsShown] = useState(true);
  // Whether an entry, a row, a step or an epic, has been opened by a click or Enter. Under 760px an opened entry's page
  // draws alone, in the list's place; at 760px and wider it changes nothing about the inbox.
  const [rowOpened, setRowOpened] = useState(false);
  // Whether the inbox is collapsed to its rail, which the inbox foot's control, the rail's mark and b set. It holds
  // through openings, views and a narrowing below 760px, where the stacked layout draws the inbox whatever it holds.
  const [inboxCollapsed, setInboxCollapsed] = useState(false);
  // The flyout open beside a collapsed inbox's rail, if any.
  const [flyout, setFlyout] = useState<Flyout | null>(null);
  // Whether the window is wide, read at mount and followed on the query's change event. A window narrowed below 760px
  // closes any flyout, and keeps rowOpened and the collapse, so widening it again shows the inbox as it was.
  const [wide, setWide] = useState(() => window.matchMedia(wideQuery).matches);
  useEffect(() => {
    const query = window.matchMedia(wideQuery);
    const follow = () => {
      setWide(query.matches);
      if (!query.matches) {
        setFlyout(null);
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
  // Whether reduced motion is asked for, read at mount and followed on the query's change event. While it is, a moved row
  // draws no mark.
  const [reduced, setReduced] = useState(() => window.matchMedia(reduceQuery).matches);
  useEffect(() => {
    const query = window.matchMedia(reduceQuery);
    const sync = () => setReduced(query.matches);
    query.addEventListener("change", sync);
    return () => {
      query.removeEventListener("change", sync);
    };
  }, []);
  // Whether the picker palette is open.
  const [pickerOpen, setPickerOpen] = useState(false);
  // The directory the palette opens in: the project's, when New session here opened it, and none for N, ⌘K and the
  // inbox's New. It is cleared once the palette is shut.
  const [pickerDir, setPickerDir] = useState("");
  useEffect(() => {
    if (!pickerOpen) {
      setPickerDir("");
    }
  }, [pickerOpen]);
  // The key of the project whose page is open, which a project's head or an address opens.
  const [projectKey, setProjectKey] = useState<string | null>(null);
  // Whether the accounts and routes page is open: the gear in the inbox foot opens it, and the address #/accounts names it.
  // It takes the conversation's place, and any navigation shuts it.
  const [accountsOpen, setAccountsOpen] = useState(false);
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
  // The answer's life for the middle conversation's own question, and the note its Ask's last call left.
  const [askState, setAskState] = useState<AskState>({ phase: "idle" });
  const [askNote, setAskNote] = useState<AskNote>(noAskNote);
  // The fixed lines an Escape leaves when taking queued messages back or interrupting a row fails, one per failure.
  const [escapeErrors, setEscapeErrors] = useState<string[]>([]);
  // The headless session rows the owner has interrupted, by row key, each with the turn_since its stream held at the press
  // (0 when no stream held one). A marked row reads interrupting on its dock's line while the list still says it is
  // working. The mark clears once the row's stream reports interrupting or a turn other than the one held, or once the
  // list no longer says working.
  const [interrupts, setInterrupts] = useState<Map<string, number>>(() => new Map());
  const [archiveError, setArchiveError] = useState("");
  // The toast's notice: the row an archive under 760px just archived, or the moves a list answer made. Each notice makes its
  // own object, so a later one replaces the toast and restarts its lifetime.
  const [toast, setToast] = useState<ToastNotice | null>(null);
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
  // The moved rows a list answer left a mark on, by row key, each with its move. A mark clears moveMs after its answer, on
  // the App's clock, and reduced motion draws none. moveTimers holds each mark's clearing timer by row key, so a row that
  // moves again restarts its own.
  const [moving, setMoving] = useState<ReadonlyMap<string, Move>>(() => new Map());
  const moveTimers = useRef(new Map<string, unknown>());
  // The projects whose cards are folded, and the projects whose ended line is open, by project key. They start unfolded
  // and shut.
  const [folded, setFolded] = useState<Set<string>>(() => new Set());
  const [endedOpen, setEndedOpen] = useState<Set<string>>(() => new Set());
  // The conversations whose ended epics are folded under their rows, open, by conversation key. They start shut.
  const [convoEndedOpen, setConvoEndedOpen] = useState<Set<string>>(() => new Set());
  // The detail each epic the poll did not load answers with when its page opens. An epic is asked for once, so the
  // answers are kept here and the asked ids in a ref.
  const [askedDetails, setAskedDetails] = useState<Map<string, EpicsGetResponse>>(() => new Map());
  const askedEpics = useRef(new Set<string>());
  // The epics whose read failed. An epic in neither the poll's detail nor the answers is still loading.
  const [failedEpicReads, setFailedEpicReads] = useState<Set<string>>(() => new Set());
  // The design records, by design id: each the newest Designs.Get answer or error of its design, which the held design's page
  // draws. A read keeps its record through setDesignRecords only when no later read of the same design has begun.
  const [designRecords, setDesignRecords] = useState<ReadonlyMap<string, DesignRecord>>(() => new Map());
  const refetchDesign = useMemo(
    () => designRefetch(clients, (id, record) => setDesignRecords((previous) => new Map(previous).set(id, record))),
    [clients],
  );
  // The epic page's own command line: the last page command that did not land, under its title row.
  const [epicCommandLine, setEpicCommandLine] = useState("");
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
  // The contract an opened address names, and the read set of the contract records, which the page keeps for its life.
  const [openedContract, setOpenedContract] = useState<OpenedContract | null>(null);
  // The design an opened press named, and whose card is held while the cursor rests on the entry it opened over.
  const [openedDesign, setOpenedDesign] = useState<OpenedDesign | null>(null);
  const [contractRead, setContractRead] = useState<ReadonlySet<string>>(() => new Set());
  // The About the middle dock holds, set by a row's control on an open contract's record, and whether the phone's sheet
  // holds the conversation over the record.
  const [about, setAbout] = useState<RowAbout | null>(null);
  const [talking, setTalking] = useState(false);
  // The worker session the page shows, held under the step it belongs to.
  const [sessionStream, setSessionStream] = useState<Stream | null>(null);
  // The list panel open beside a session, and the thing a panel held last, which ] reopens.
  const [listPanel, setListPanel] = useState<ListOpen | null>(null);
  const [held, setHeld] = useState<Held | null>(null);
  // The motion of the held layout (D16), and what the last commit drew of it. The panes hold the custom properties a move reads.
  const [motion, setMotion] = useState<HeldMotion>(noMotion);
  const shownDesk = useRef<DeskShown | null>(null);
  const panesRef = useRef<HTMLDivElement>(null);
  // The epic page section, and the scroll place a back restores once the page it returns to is drawn.
  const pageRef = useRef<HTMLElement>(null);
  const restorePageScroll = useRef<number | null>(null);
  // loadArtifact reads a run's log or artifact for the epic page. It is stable across renders, so an open log or image
  // is not loaded again on every poll.
  const loadArtifact = useCallback(
    (run: string, name: string, stderr: boolean) => clients.evidence.artifact({ run, name, stderr }),
    [clients],
  );
  // The messages each row's composer sent that the row's stored conversation does not hold yet, by row key. Each shows
  // as sending in its row's reader until its stored prompt arrives.
  const [sendingByRow, setSendingByRow] = useState<Map<string, Sending[]>>(() => new Map());
  const seenWatch = useRef<ReturnType<typeof watchSeen>>();
  // The row or ended epic the seen watch last rested on, which its write marks seen.
  const restedSeen = useRef<Row | EpicItem | null>(null);
  // The conversation row or epic the cursor rested on at the last commit, when the inbox drew it unarchived. A list answer
  // that archives it leaves it drawn while the cursor rests on it.
  const drawnOpen = useRef<Row | EpicItem | null>(null);
  // The spots the last rows answer drew each conversation at, in the inbox and the Archived view, which the next answer's
  // moves are read against. It is null until the first answer.
  const lastSpots = useRef<Map<string, Placed> | null>(null);

  // The address App reports. lastForm is the form it last reported or applied. cause is the mode the next address change
  // takes: push for an open, a crumb return or the first cursor key after one, replace for a later cursor key. A cause
  // is set together with a render of its own, and the report of that render consumes it, so a cause that changes no
  // address is never carried to a later change. armed is whether the next cursor key pushes, and engaged whether any
  // cause has come since mount, which a change with no cause waits for. applied is set when an address is applied, and
  // the report after it takes the form App shows without reporting it. pendingOpen is the entry an address names, with its
  // view, until it is applied or dropped, and pageCursor is the cursor key the epic page was last reset for.
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
  // An opening closes any flyout, and leaves the inbox as it was.
  const navigated =
    <A extends unknown[]>(action: (...args: A) => void) =>
    (...args: A) => {
      cause.current = "push";
      arm();
      renderAddress();
      setFlyout(null);
      // An opening leaves the accounts page, so the conversation's place is the entry's again.
      setAccountsOpen(false);
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
      setFlyout(null);
      // A cursor move leaves the accounts page too, as an opening does.
      setAccountsOpen(false);
      action(...args);
    };
  // adopt records an applied address. It reports nothing, and arms the next cursor key. Its render is the one whose
  // report takes the applied form, so that form is never left waiting for a later change.
  const adopt = () => {
    applied.current = true;
    arm();
    renderAddress();
  };

  const words = (searchText ?? "")
    .toLowerCase()
    .split(/\s+/)
    .filter((word) => word !== "");
  // The open entry the inbox keeps drawn though a list answer archived or ended it, by key, or "" when none. The inbox
  // reads it as unarchived and not ended, in its old place; the Archived view lists it as the answer says.
  const keptKey = heldOpenKey(drawnOpen.current, cursor.key, view, rows, epics, rowMarks, epicMarks);
  const shownRows = rows.map((row) =>
    readAsSeen(withRowMark(row.key === keptKey ? { ...row, archived: false, ended: false } : row, rowMarks), seenWritten),
  );
  // The epics as the local archive marks leave them, which the views draw in place of the list's.
  const marked = withEpicMarks(epics, epicMarks);
  // The listed epics, each with the local archive mark applied to its archived field, which the tree places. The open
  // entry a list answer archived reads as unarchived while it is held.
  const treeEpics = [...epics.listed.values()].map((epic) => {
    const mark = epicMarks.get(epic.id);
    const archived = mark ?? (`epic:${epic.id}` === keptKey ? false : epic.archived);
    return archived === epic.archived ? epic : { ...epic, archived };
  });
  // The tree of the view before the chips and the search narrow it: each project's conversations, with the epics and
  // contracts that hang under them.
  const treeOf = (shown: View) =>
    buildTree({
      rows: shownRows,
      epics: treeEpics,
      details: epics.details,
      contracts,
      designs,
      host: host?.host ?? "",
      view: shown,
    });
  const tree = treeOf(view);
  // The rail's tiles come from the inbox's tree whatever view the list shows, so a project keeps its tile and count
  // in the Archived view.
  const railTree = view === "inbox" ? tree : treeOf("inbox");
  const counts = countsOf(tree);
  // Each project's waiting number over the whole tree, before the chips and the search narrow it: a folded card says it.
  const waitingOf = new Map(tree.map((project) => [project.key, projectWaiting(project)]));
  const projects = searchTree(narrowTree(tree, view === "inbox" ? narrowBy : ""), words);
  // The counted things of the projects as they are narrowed and searched, oldest first, which w walks.
  const waitKeys = waitOrder(projects);
  // A chip whose count reaches zero turns its narrowing off, so the tree is not left narrowed to what it cannot show.
  useEffect(() => {
    if ((narrowBy === "waiting" && counts.waiting === 0) || (narrowBy === "new" && counts.fresh === 0)) {
      setNarrowBy("");
    }
  }, [narrowBy, counts.waiting, counts.fresh]);
  // The inbox is collapsed to its rail only at 760px and wider: under 760px the stacked layout draws it, whatever the
  // collapse holds.
  const collapsed = wide && inboxCollapsed;
  // The Archived head's count: the archived conversations and the archived epics.
  const archivedCount =
    shownRows.filter((row) => row.archived).length + marked.archived.filter((item) => item.key !== keptKey).length;
  const flat = treeEntries(projects, folded, endedOpen, convoEndedOpen);
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
  // The epic's steerer: the row of the session that steers it, as the epics list names it. The middle row is the row the
  // reader column draws: the cursor's row, or for an epic entry its steerer's row. The entry the reader follows is the
  // middle row, or else the cursor's step. Its stream is kept under this key, so a stream never shows under another entry.
  // Under 760px the cursor's entry is the address only once it is opened, so the inbox's address names no row.
  const pageDrawn = wide || rowOpened;
  // The project whose page is open, while a page is drawn. The project page takes the page area, so the cursor's panels
  // do not draw while it is shown, and neither does a contract's.
  const shownProject = pageDrawn ? projectKey : null;
  // The collapsed inbox's rail draws a tile per project of the inbox, whatever the view, and the tile of the project that
  // holds the open page is current. The flyout is drawn only while the inbox is collapsed, on a project the rail draws.
  const flyoutProject = collapsed && flyout !== null ? railTree.find((project) => project.key === flyout.key) : undefined;
  const flyoutModel = flyoutProject === undefined ? null : flyoutOf(flyoutProject, now);
  // The contract the address opened is open while the cursor rests on the entry it opened over and no project page is
  // shown. Its conversation is the row whose session is the contract's conversation: the middle while that row is live,
  // which an archived row, or one stopped or failed, is not.
  const openedId = shownProject === null && openedContract?.cursor === cursorKey ? openedContract.id : "";
  const contract = openedId === "" ? undefined : contracts.find((candidate) => candidate.id === openedId);
  const contractShown = contract !== undefined;
  const contractRow = contract === undefined ? null : steererRowOf(shownRows, contract.conversation);
  const contractLiveRow =
    contractRow !== null && !contractRow.archived && !endedStates.includes(contractRow.state) ? contractRow : null;
  // The design whose card is held: the one an opened press named, while the cursor rests on the entry it opened over and no
  // project page is shown. Its conversation is the middle while that row is live, as a contract's is.
  const openedDesignId = shownProject === null && openedDesign?.cursor === cursorKey ? openedDesign.id : "";
  const openedDesignFound = openedDesignId === "" ? undefined : designs.find((candidate) => candidate.id === openedDesignId);
  const designRow = openedDesignFound === undefined ? null : steererRowOf(shownRows, openedDesignFound.conversation);
  const designLiveRow =
    designRow !== null && !designRow.archived && !endedStates.includes(designRow.state) ? designRow : null;
  // A design is held at the desk whatever its conversation; under 760px it is the sheet only over a live conversation (D9),
  // and with none the inbox draws it as before.
  const design = openedDesignFound !== undefined && (wide || designLiveRow !== null) ? openedDesignFound : undefined;
  const designShown = design !== undefined;
  // The About and the talk belong to the open contract: closing it, opening another, or moving the cursor off its entry
  // clears them.
  const shownContractId = contract?.id ?? "";
  useEffect(() => {
    setAbout(null);
    setTalking(false);
  }, [shownContractId]);
  const listedSteering = epicEntry === null ? "" : (epics.listed.get(epicEntry.id)?.steering ?? "");
  const steering =
    steered !== null && epicEntry !== null && steered.epic === epicEntry.id && steered.listed === listedSteering
      ? steered.session
      : listedSteering;
  const steererRow = epicEntry === null || contractShown || designShown ? null : steererRowOf(rows, steering);
  const middleRow = designShown ? designLiveRow : contractShown ? contractLiveRow : (cursorRow ?? steererRow);
  // The row the seen watch rests on: the cursor's row, or while a contract is open, the conversation the middle draws.
  // The accounts page covers the conversation, so the seen watch rests on no row while it is open.
  const watchedRow = accountsOpen ? null : designShown ? designLiveRow : contractShown ? contractLiveRow : cursorRow;
  const middleKey = middleRow?.key ?? "";
  const readerKey = middleKey !== "" ? middleKey : cursorStepKey;
  // The cursor's epic entry while its ending reads unseen: the seen watch rests on it, and its write marks it seen.
  const restedEpic =
    epicEntry !== null && unseenEnding(epicEntry, epics.listed.get(epicEntry.id), seenEpics) ? epicEntry : null;
  const stepEntries = flat.filter((item): item is StepItem => item.kind === "step");
  const epicDetail =
    epicEntry === null ? undefined : newerDetail(epics.details.get(epicEntry.id), askedDetails.get(epicEntry.id));
  // The history is read for the epic entry the cursor rests on, once its detail is held. It is read again when the
  // epic's last write moves.
  const historyId = epicEntry !== null && epicDetail !== undefined ? epicEntry.id : "";
  const epicChanged = epicDetail?.epic?.changed ?? 0n;
  const shownIndex = Math.max(index, 0);
  // The step whose worker session the open page shows: the session page's step, or on the phase page the brief step of
  // its task, whose conversation the record reads its brief from. Its session is followed only while the epic's detail
  // holds the step, so a step the detail lacks opens no stream.
  const openStep = !epicPage.closed && epicPage.page.kind === "session" ? epicPage.page.step : "";
  const briefed =
    !epicPage.closed && epicPage.page.kind === "phase" && epicDetail !== undefined
      ? (briefStepOf(epicDetail, epicPage.page.task)?.id ?? "")
      : "";
  const sessionStep = openStep !== "" ? openStep : briefed;
  const shownSession = epicDetail?.steps.some((step) => step.id === sessionStep) === true ? sessionStep : "";
  // The epic entry the cursor rests on, when the poll did not load its detail, is asked for once with Epics.Get. A
  // failed answer leaves the page at its could-not-read line.
  const askedId = epicEntry?.id ?? "";
  const polledDetail = askedId !== "" && epics.details.has(askedId);
  // The address the page shows. The epic page's reset to the open page is a layout effect of the render that moves the
  // cursor. So a render that moves the cursor reads the open page, and the page the cursor left is shown only once that
  // reset has run.
  const shownPage = pageCursor.current === cursorKey ? epicPage : openEpicPage;
  // The list panel the cursor's session row shows: a list is open only while the cursor rests on the row it is open on.
  const listShown: ListPanelKind | null =
    shownProject === null &&
    !contractShown &&
    !designShown &&
    !accountsOpen &&
    listPanel !== null &&
    listPanel.key === cursorRowKey
      ? listPanel.panel
      : null;
  // A shown project page is the address, then an open contract, which no project page shows over, and otherwise the
  // cursor's entry.
  const shownAddress: Address = {
    view,
    entry:
      shownProject !== null
        ? { kind: "project", key: shownProject }
        : contract !== undefined
          ? { kind: "contract", id: contract.id }
          : accountsOpen
            ? { kind: "accounts" }
            : pageDrawn
              ? shownEntry(cursorItem, shownPage, listShown)
              : null,
  };
  const shownForm = formatAddress(shownAddress);

  // The held cursor follows the entry shown, so a first render or a clamp does not leave it on a stale key.
  useEffect(() => {
    if (cursor.key !== cursorKey || cursor.index !== shownIndex) {
      setCursor({ key: cursorKey, index: shownIndex });
    }
  }, [cursor, cursorKey, shownIndex]);

  // Each commit records the cursor's row or epic when the inbox drew it unarchived, which the next list answer reads. A
  // step, a contract, or an entry drawn archived records nothing.
  useLayoutEffect(() => {
    drawnOpen.current =
      cursorItem !== null && (cursorItem.kind === "epic" || isRow(cursorItem)) && !cursorItem.archived
        ? cursorItem
        : null;
  });

  // The contract panel shuts once the cursor leaves the entry it opened over, so a return to that entry does not reopen
  // it.
  useEffect(() => {
    if (openedContract !== null && openedContract.cursor !== cursorKey) {
      setOpenedContract(null);
    }
  }, [openedContract, cursorKey]);

  // The design card shuts once the cursor leaves the entry it opened over, as the contract panel does.
  useEffect(() => {
    if (openedDesign !== null && openedDesign.cursor !== cursorKey) {
      setOpenedDesign(null);
    }
  }, [openedDesign, cursorKey]);

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

  // The record's brief step is paged back while its conversation holds no brief and has older items: each page is asked
  // for once the last one is drawn, until the brief is held or no older item is left.
  useEffect(() => {
    if (briefed === "" || sessionStream === null || sessionStream.key !== briefed) {
      return;
    }
    const { conversation } = sessionStream;
    if (briefOf(conversation) === undefined && conversation.more) {
      sessionOlder.current();
    }
  }, [sessionStream, briefed]);

  useEffect(() => {
    setVisited((previous) => keepEntries(previous, rows, middleRow));
  }, [rows, middleKey]);

  useEffect(() => {
    setVisitedSteps((previous) => keepEntries(previous, stepEntries, cursorStep));
  }, [epics, cursorStepKey]);

  useEffect(() => {
    setPinnedRows((previous) => keepEntries(previous, rows, pinned));
  }, [rows, pinned]);

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

  // readEpic asks the server for an epic. Its answer is the detail the page draws, and a failed read is kept as failed.
  const readEpic = (id: string): void => {
    void clients.epics.get({ id }).then(
      (answer) => setAskedDetails((previous) => new Map(previous).set(id, answer)),
      () => setFailedEpicReads((previous) => new Set(previous).add(id)),
    );
  };
  // rereadEpic reads an epic again after an owner call. Its answer is kept as the detail the page draws, and the read
  // resolves true; a failed read resolves false.
  const rereadEpic = (id: string): Promise<boolean> =>
    clients.epics.get({ id }).then(
      (answer) => {
        setAskedDetails((previous) => new Map(previous).set(id, answer));
        return true;
      },
      () => false,
    );
  // retryEpicRead reads an epic whose read failed once more. The page shows it as loading until that read answers.
  const retryEpicRead = (id: string): void => {
    setFailedEpicReads((previous) => new Set([...previous].filter((failed) => failed !== id)));
    readEpic(id);
  };
  // An epic the poll did not load is asked for once, when the cursor rests on it, and its answer draws the page.
  useEffect(() => {
    if (askedId === "" || polledDetail || askedEpics.current.has(askedId)) {
      return;
    }
    askedEpics.current.add(askedId);
    readEpic(askedId);
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
  const viewed = cursorItem !== null && cursorItem.kind !== "contract" ? cursorItem : null;
  useEffect(() => {
    onViewing?.(viewingOf(viewed));
  }, [viewed?.kind, viewed?.id]);

  // A back restores the scroll place of the page it returns to before that page is painted.
  useLayoutEffect(() => {
    if (restorePageScroll.current !== null && pageRef.current !== null) {
      pageRef.current.scrollTop = restorePageScroll.current;
    }
    restorePageScroll.current = null;
  }, [epicPage]);

  // A search field opened by / or the head's control takes its focus once the render that draws it has run.
  useLayoutEffect(() => {
    if (focusSearch.current && inputRef.current !== null) {
      focusSearch.current = false;
      inputRef.current.focus();
    }
  });

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
  // an epic's id. A pane with no orrery_id has no archive kind, so it does nothing. A landed call marks the entry as
  // the call left it, keyed by the entry's key, until the next list answer; a failed call leaves the entry where it
  // was. A refused call goes to refused with the server's reason when refused is given, and otherwise to the list's
  // line.
  const setArchive = (entry: Row | EpicItem, archived: boolean, refused?: (reason: string) => void) => {
    const kind = entry.kind === "epic" ? "epic" : entry.archiveKind;
    if (kind === "none") {
      return;
    }
    const request = { kind, id: entry.kind === "epic" ? entry.id : entry.archiveId };
    const write = () => (archived ? clients.epics.archive(request) : clients.epics.unarchive(request));
    const landed = () => {
      if (entry.kind === "epic") {
        setEpicMarks((previous) => new Map(previous).set(entry.id, archived));
      } else {
        setRowMarks((previous) => new Map(previous).set(entry.key, archived));
        if (archived && !wide) {
          setToast({ kind: "archived", row: entry });
        }
      }
    };
    if (refused === undefined) {
      void lineWrite(write, archiveFailedText, setArchiveError, landed);
    } else {
      void write().then(landed, (error: unknown) => refused(ConnectError.from(error).rawMessage));
    }
  };

  // The toast goes toastMs after it shows, on the App's clock, and a later notice restarts that count.
  // Unmount or its replacement clears the timer.
  const toastClock = seenClock ?? realClock;
  useEffect(() => {
    if (toast === null) {
      return;
    }
    const timer = toastClock.setTimeout(() => setToast(null), toastMs);
    return () => toastClock.clearTimeout(timer);
  }, [toast, toastClock]);
  // A list answer is read against the last: each conversation it draws at another spot has moved, and is marked for moveMs
  // on the App's clock. The open entry the answer holds in its place does not move, and neither does a row the owner moved
  // from this page. The first answer moves nothing. The moves an answer makes are one move notice, which replaces the toast
  // or notice before it.
  useEffect(() => {
    const spots = spotsOfAnswer(rows, epics, contracts, designs, host?.host ?? "");
    const before = lastSpots.current;
    lastSpots.current = spots;
    if (before === null) {
      return;
    }
    const moves = movesBetween(before, spots).filter((move) => move.row.key !== keptKey && !rowMarks.has(move.row.key));
    if (moves.length === 0) {
      return;
    }
    setToast({ kind: "moved", moves });
    setMoving((previous) => {
      const next = new Map(previous);
      for (const move of moves) {
        next.set(move.row.key, move);
      }
      return next;
    });
    for (const move of moves) {
      const key = move.row.key;
      const pending = moveTimers.current.get(key);
      if (pending !== undefined) {
        toastClock.clearTimeout(pending);
      }
      moveTimers.current.set(
        key,
        toastClock.setTimeout(() => {
          moveTimers.current.delete(key);
          setMoving((previous) => {
            if (!previous.has(key)) {
              return previous;
            }
            const next = new Map(previous);
            next.delete(key);
            return next;
          });
        }, moveMs),
      );
    }
  }, [rows]);
  // Unmount clears the timers the moves left.
  useEffect(() => {
    const timers = moveTimers.current;
    return () => {
      for (const handle of timers.values()) {
        toastClock.clearTimeout(handle);
      }
      timers.clear();
    };
  }, []);
  // Undo unarchives each row the toast names, through the same call an Unarchive from its menu makes, which leaves a row
  // with no archive kind alone, and takes the toast away.
  const undoToast = () => {
    if (toast === null) {
      return;
    }
    setToast(null);
    const named = toast.kind === "archived" ? [toast.row] : toast.moves.map((move) => move.row);
    for (const row of named) {
      setArchive(row, false);
    }
  };
  const undoAction = { label: "Undo", onAction: undoToast };

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

  // Each render tells the watch the cursor's row or unseen ended epic, and whether it reads as unseen. A changed row is a
  // new key, so a change made while the cursor rests on it gets its own dwell; an ending is a key of its own.
  useEffect(() => {
    restedSeen.current = watchedRow ?? restedEpic;
    if (restedEpic !== null) {
      seenWatch.current?.rest(endingKey(restedEpic), true);
    } else {
      seenWatch.current?.rest(
        watchedRow === null ? "" : `${watchedRow.key}@${watchedRow.changed}`,
        watchedRow?.unseen ?? false,
      );
    }
  });

  // The conversation the cursor's reader holds, only when its stream belongs to the entry the cursor rests on.
  const cursorConversation = stream !== null && stream.key === readerKey ? stream.conversation : null;
  // The conversation the pinned reader holds, only when its stream belongs to the pinned row.
  const pinnedConversation =
    pinStream !== null && pinned !== null && pinStream.key === pinned.key ? pinStream.conversation : null;
  // The middle conversation's own question, and the key that names it for the answer's life and the Ask's calls. The
  // key is null while the stream shows no question.
  const cursorAsk = middleRow === null ? null : conversationAsk(middleRow, cursorConversation);
  const cursorAskKey = middleRow === null || cursorAsk === null ? null : askKey(middleRow, cursorAsk);
  // The answer's life starts idle for each middle conversation. Once its stream is live, the life moves on with the
  // question the stream shows: a sent answer folds to its line, and a refused one clears when the question changes.
  useEffect(() => {
    setAskState({ phase: "idle" });
    setAskNote(noAskNote);
  }, [middleKey]);
  // shownAsk is the question the live stream shows now, which a reply that lands after the stream moved on reads.
  const shownAsk = useRef<{ live: boolean; key: string | null }>({ live: false, key: null });
  shownAsk.current = { live: cursorConversation !== null, key: cursorAskKey };
  useEffect(() => {
    if (cursorConversation === null) {
      return;
    }
    setAskState((previous) => observe(previous, cursorAskKey));
    setAskNote((previous) => (previous.key === cursorAskKey ? previous : noAskNote));
  }, [cursorConversation, cursorAskKey]);
  // answerCursor answers the middle conversation's own question with its option choice: a tap and a digit both come
  // here. The press starts the answer and the server's reply settles it. A digit past the last option, or a press while
  // an answer is in flight, sends nothing. While an epic is open in the panel its question's cards take the digits, so
  // none answers the steering conversation beside it.
  const answerCursor = (choice: number) => {
    if (middleRow === null || cursorAsk === null || cursorAskKey === null || (epicEntry !== null && !epicPage.closed)) {
      return;
    }
    const option = cursorAsk.options.at(choice - 1);
    // The answered line draws after the item that is newest now, so the items that arrive after the press draw after it.
    const started =
      option === undefined ? null : press(askState, cursorAskKey, option, cursorAsk.question, newestItemKey(cursorConversation));
    if (started === null) {
      return;
    }
    const row = middleRow;
    const key = cursorAskKey;
    const conversation = stream !== null && stream.key === row.key ? stream.conversation : null;
    setAskState(started);
    void actions
      .answer(clients, row, conversation, choice)
      .catch(() => false)
      .then((sent) =>
        setAskState((previous) => {
          if (previous.phase !== "sending" || previous.key !== key) {
            return previous;
          }
          // The stream may have dropped the question before the reply landed: the settled answer reads it now.
          const settled = settle(previous, sent);
          return shownAsk.current.live ? observe(settled, shownAsk.current.key) : settled;
        }),
      );
  };
  // askCall makes one of the middle question's own calls against its target. A refused call's reason becomes the Ask's
  // note; a call the server took draws nothing, since the stream redraws the question.
  const askCall = (call: (target: AskTarget) => Promise<string>) => {
    const target = cursorAsk?.target ?? null;
    if (target === null || cursorAskKey === null) {
      return;
    }
    const key = cursorAskKey;
    void call(target).then((reason) => setAskNote({ key, text: reason }));
  };
  // The cursor column's Ask: its answer, its note and its calls. Archive this session archives the middle row, as the
  // inbox menu does, and a refusal's reason is the Ask's note.
  const askCalls: AskCalls = {
    state: askState,
    note: askNote,
    onAnswer: answerCursor,
    onNotNow: () => askCall((target) => actions.notNow(clients, target)),
    onBringBack: () => askCall((target) => actions.bringBack(clients, target)),
    onDecline: () => askCall((target) => actions.decline(clients, target)),
    onArchive: () => {
      if (middleRow === null || cursorAskKey === null) {
        return;
      }
      const key = cursorAskKey;
      setArchive(middleRow, true, (reason) => setAskNote({ key, text: reason }));
    },
  };
  // The More menu of a list row: its items, each acting on that row alone. Mark seen is drawn while the row is unseen, and
  // Archive, or Unarchive in the Archived view, while the row can be archived. Open in a new window opens the row's
  // address in a new window, in the view the page shows. Under 760px the menu is a sheet, under the row's title the line
  // of its project, state in words and age.
  const rowMore = (row: Row): RowMore => ({
    open: openMenu === row.key,
    toggle: () => setMenuKey((current) => (current === row.key ? null : row.key)),
    close: () => setMenuKey(null),
    sheet: wide
      ? undefined
      : [lastPathElement(row.project), stateWord(row.state), ageText(row.changed, now)]
          .filter((part) => part !== "")
          .join(" · "),
    items: [
      {
        label: "Open in a new window",
        icon: "open",
        onSelect: () => {
          window.open(formatAddress({ view, entry: shownEntry(row, openEpicPage, null) }), "_blank");
        },
      },
      ...(row.unseen
        ? [{ label: "Mark seen", icon: "check" as const, kbd: "x", onSelect: () => void writeSeen(row) }]
        : []),
      ...(row.archiveKind === "none"
        ? []
        : [
            "sep" as const,
            {
              label: view === "archived" ? "Unarchive" : "Archive",
              icon: "archive" as const,
              onSelect: () => setArchive(row, view !== "archived"),
            },
          ]),
    ],
  });
  // The conversations the readers draw, each without the prompts its queue still holds or has discarded, and the worker
  // session's, shown the same way.
  const cursorShown = useMemo(() => shownConversation(cursorConversation), [cursorConversation]);
  const pinnedShown = useMemo(() => shownConversation(pinnedConversation), [pinnedConversation]);
  const sessionShown = useMemo(() => shownConversation(sessionStream?.conversation ?? null), [sessionStream]);
  // A column's find bar shows only while its row is the column's. Its matches are the query's over the column's
  // conversation, and its current match is clamped to them where it is read.
  const cursorFinding = cursorFind !== null && cursorFind.key === middleKey ? cursorFind : null;
  const cursorMatches = useMemo(
    () => findMatches(cursorShown?.items ?? [], cursorFinding?.query ?? "", cursorShown?.unfinished ?? []),
    [cursorShown, cursorFinding?.query],
  );
  const cursorCurrent = cursorFinding === null ? -1 : findCurrent(cursorFinding.current, cursorMatches.length);
  const pinnedFinding = pinnedFind !== null && pinnedFind.key === pinnedKey ? pinnedFind : null;
  const pinnedMatches = useMemo(
    () => findMatches(pinnedShown?.items ?? [], pinnedFinding?.query ?? "", pinnedShown?.unfinished ?? []),
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
    setEscapeErrors(failures);
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
  // Under 760px, on an opened page at its first crumb, it is Back: the epic page or the row's or step's page closes to
  // the inbox. At 760px and wider, at a page's first crumb, it does nothing.
  const backOneCrumb = () => {
    if (epicEntry !== null && epicPage.page.kind !== "epic") {
      back();
    } else if (!wide) {
      if (rowOpened) {
        if (epicEntry !== null) {
          closeEpicPage();
        } else {
          backToInbox();
        }
      }
    }
  };
  // openPhase opens a task's record over the epic page. On a record, a task's chip opens that task's record in place.
  const openPhase = navigated((task: string, phase: Phase) => {
    if (epicPage.page.kind === "phase") {
      setEpicPage((previous) => ({ ...previous, page: { kind: "phase", task, phase } }));
      return;
    }
    drill({ kind: "phase", task, phase });
  });
  const openContract = navigated(() => drill({ kind: "contract" }));
  // closeEpicPage closes the epic page. Under 760px the epic page is the opened page, so closing it returns to the inbox.
  const closeEpicPage = navigated(() => {
    setEpicPage(closedEpicPage);
    if (!wide) {
      backToInbox();
    }
  });

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
      page: { kind: "session", task: step.task, phase: step.readOnly ? "reviewing" : "implementing", step: step.id },
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
    setProjectKey(null);
    setCursor({ key: flat[next].key, index: next });
  };

  // selectItem moves the cursor to an entry the list shows, as a click on that entry does. It is an open.
  const selectItem = navigated((item: Item) => {
    moveTo(flat.findIndex((candidate) => candidate.key === item.key));
  });

  // openView sets the view, as the v key and the foot's Archived and Back to the inbox do. It is an open.
  const openView = navigated(setView);

  // App reports its address after each render, once, when the form it shows differs from the one it last reported or
  // applied. An address still pending, or an epic sub-page still landing, reports nothing and leaves its cause to the
  // render that settles it. This effect comes before the effects that set a cause or apply an address, so a cause they
  // set reaches the next render's report, and an address they apply does not reach this one. It is a layout effect, so
  // it reports in the commit of the render it reports: a passive effect could run after a key press has set a cause,
  // and consume that cause against the render before it.
  useLayoutEffect(() => {
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

  // revealKey opens what hides the entry under key: its project's card when the card is folded, its ended line when the
  // entry sits behind that line, and its conversation's ended fold when the entry is one of its folded epics. Then the
  // entry is drawn, so the cursor can rest on it.
  const revealKey = (key: string) => {
    for (const project of tree) {
      const behind = project.ended.some((convo) => holdsKey(convo, key));
      if (behind || project.convos.some((convo) => holdsKey(convo, key))) {
        setFolded((previous) => withMember(previous, project.key, false));
      }
      if (behind) {
        setEndedOpen((previous) => withMember(previous, project.key, true));
      }
      for (const convo of project.convos) {
        if (convo.folded.some((hung) => holdsThing(hung, key))) {
          setConvoEndedOpen((previous) => withMember(previous, convo.key, true));
        }
      }
    }
  };

  // waitTo moves the cursor to a counted thing that w reached, revealing it first. The cursor finds it by key once the
  // list is drawn again.
  const waitTo = (key: string) => {
    revealKey(key);
    setProjectKey(null);
    setCursor({ key, index: shownIndex });
  };

  // openEpic moves the cursor to an epic's entry, after revealing it. The entry is then in the list, so the cursor rests
  // on it. Under 760px the epic's page draws alone, so the entry is opened.
  const openEpic = (id: string) => {
    revealKey(`epic:${id}`);
    setProjectKey(null);
    setCursor({ key: `epic:${id}`, index: shownIndex });
    if (!wide) {
      openPageAlone();
    }
  };

  // showMove is the move notice's Show: it opens the place of a moved row and rests the cursor on the row. Archived switches
  // the view to Archived, and the row takes the cursor once that view's list holds it. The inbox's card or ended line opens
  // through revealKey, as waitTo opens it. Under 760px the list draws alone, so an open page or epic page closes first.
  const showMove = navigated((move: Move) => {
    setToast(null);
    setProjectKey(null);
    if (!wide) {
      setRowOpened(false);
      setEpicPage(closedEpicPage);
      setOpenedContract(null);
    }
    if (move.to.line === "archived") {
      setView("archived");
      setPending({ kind: move.row.kind, id: move.row.id });
      return;
    }
    setView("inbox");
    waitTo(move.row.key);
  });

  // openPageAlone opens the cursor's page under 760px, where it draws alone: a row's page, or an epic page that a Back
  // closed while the cursor stayed on it.
  const openPageAlone = () => {
    setRowOpened(true);
    setEpicPage((previous) => (previous.closed ? openEpicPage : previous));
  };

  // openProject opens a project's page from its head or its address: the address names the project, and the page draws
  // in the page area. An open contract's panel or design's card shuts, since the page takes its place. It is a navigation.
  const openProject = navigated((key: string) => {
    setOpenedContract(null);
    setOpenedDesign(null);
    setProjectKey(key);
    setRowOpened(true);
  });

  // openAccounts opens the accounts and routes page, from the inbox foot's gear. The page takes the conversation's place, so an
  // open project page, contract or design card shuts. It is a navigation.
  const openAccounts = navigated(() => {
    setProjectKey(null);
    setOpenedContract(null);
    setOpenedDesign(null);
    setAccountsOpen(true);
  });

  // openProjectItem is the press a project page's conversation or WorkCard makes. It opens what the tree opens for the
  // item key, as openEpic does, and shuts the page. A key the tree draws no entry of opens nothing, as the tree's own press
  // does. A contract's key opens the contract's panel, as the tree's press on its item does. A thing the
  // chip or the search keeps out of the list is let back in first, by clearing them, so the cursor rests on the thing
  // pressed and not on a neighbour.
  const openProjectItem = navigated((key: string) => {
    const convos = tree.flatMap((project) => [...project.convos, ...project.ended]);
    if (!convos.some((convo) => holdsKey(convo, key))) {
      return;
    }
    const hung = convos.flatMap((convo) => [...convo.hung, ...convo.folded]).find((candidate) => candidate.key === key);
    if (hung?.kind === "contract") {
      openContractPanel(hung.contract.id);
      return;
    }
    if (!projects.some((project) => [...project.convos, ...project.ended].some((convo) => holdsKey(convo, key)))) {
      setNarrowBy("");
      setSearchText(null);
    }
    revealKey(key);
    setProjectKey(null);
    setCursor({ key, index: shownIndex });
    if (!wide) {
      openPageAlone();
    }
  });

  // openPickerIn opens the palette on a project's directory, which New session here does.
  const openPickerIn = (dir: string) => {
    setPickerDir(dir);
    setPickerOpen(true);
  };

  // selectKey moves the cursor to the entry drawn under key, as a press on that entry does. A press on an epic or a step
  // under 760px opens it, as a press on a row does. A press on a contract opens its panel, as its work link does. A press on
  // a design's tab opens its card at the desk, and under 760px does nothing, as it did before the card.
  const selectKey = (key: string) => {
    if (key.startsWith("design:")) {
      if (wide) {
        navigated(openDesign)(key.slice("design:".length));
      }
      return;
    }
    const item = flat.find((candidate) => candidate.key === key);
    if (item === undefined) {
      return;
    }
    if (item.kind === "contract") {
      navigated(openContractPanel)(item.contract.id);
      return;
    }
    selectItem(item);
    if (!wide) {
      openPageAlone();
    }
  };

  // openSearchField sets the search field's text, and the field opens when the text is not null. A field that is not
  // drawn yet takes its focus in the layout effect after the render that draws it.
  const openSearchField = (text: string | null) => {
    if (text !== null && inputRef.current === null) {
      focusSearch.current = true;
    }
    setSearchText(text);
  };


  // openEpicStep opens a step of an epic: the epic's entry, and the page of the step's worker session over the epic page.
  const openEpicStep = (epic: string, step: string) => {
    openEpic(epic);
    setSteppedTo({ epic, step });
  };
  // openContractPanel opens a contract's panel, as its address does: the conversation's row takes the cursor when the
  // list shows it, as an epic's line does, so the middle, the composer and the seen watch rest on the same row. The
  // contract stays open over the cursor's key that results. A project page open in the page area shuts, since the panel
  // takes its place.
  const openContractPanel = (id: string) => {
    setProjectKey(null);
    const listed = contracts.find((candidate) => candidate.id === id);
    const conversation = listed === undefined ? null : steererRowOf(shownRows, listed.conversation);
    const conversationItem =
      conversation === null ? undefined : flat.find((candidate) => candidate.key === conversation.key);
    if (conversationItem !== undefined) {
      selectItem(conversationItem);
    }
    setOpenedContract({ id, cursor: conversationItem?.key ?? cursorKey });
    setOpenedDesign(null);
    setRowOpened(true);
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
    setView(address.view);
    // An address that names no project shuts the project page, as a move of the cursor does. The contract panel shuts
    // too, and an address that names a contract opens it again once the list holds it.
    if (address.entry?.kind !== "project") {
      setProjectKey(null);
    }
    setOpenedContract(null);
    setOpenedDesign(null);
    // The accounts page is drawn from no list, so its address is applied at once, and holds no pending entry.
    const accountsAddress = address.entry?.kind === "accounts";
    setAccountsOpen(accountsAddress);
    pendingOpen.current = address.entry === null || accountsAddress ? null : { view: address.view, entry: address.entry };
    if (address.entry === null && !first && epicEntry !== null) {
      setEpicPage(closedEpicPage);
    }
    if (address.entry === null && !wide) {
      setRowOpened(false);
    }
    if (address.entry === null || accountsAddress) {
      adopt();
    }
  }, [addressForm]);

  // The pending entry is decided in the first pass whose view is its address's view, so a pass still on the old view
  // decides nothing. Until the loaded list holds the entry, App keeps waiting: the rows and epics it is given, unfiltered
  // and unmarked, are the check start() makes. Once they hold it, the view either shows it or hides it. A shown entry
  // opens: a row or step moves the cursor to its key and opens the row, as a click does, with no seen write and no reply
  // box, and an epic opens the line that holds it and moves the cursor to it, with its sub-page landing in the layout
  // effect above. A hidden one, by the filter or a local archive mark, is dropped: the cursor stays where it is, and App
  // reports its shown address with replace, arming the next cursor key as an applied address does. This effect is
  // declared after the held cursor's effect, so its cursor move wins over that effect in the commit it applies.
  useEffect(() => {
    const pending = pendingOpen.current;
    if (pending === null || pending.view !== view) {
      return;
    }
    if (!listHolds(pending, rows, epics, contracts)) {
      return;
    }
    // A project's page waits for the first load, so it does not say the tree holds nothing of the project before the
    // tree holds it.
    if (pending.entry.kind === "project" && !answered) {
      return;
    }
    const { entry } = pending;
    pendingOpen.current = null;
    if (entry.kind === "project") {
      setProjectKey(entry.key);
      setRowOpened(true);
      adopt();
      return;
    }
    if (entry.kind === "contract") {
      openContractPanel(entry.id);
      adopt();
      return;
    }
    if (entry.kind === "epic") {
      if (listHolds(pending, rows, marked)) {
        openEpic(entry.id);
        setOpened({ entry });
        setRowOpened(true);
        adopt();
        return;
      }
    } else if (entry.kind !== "accounts") {
      // The accounts page opens in the address effect, which applies its address at once, so no pending entry is one.
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
  }, [addressForm, flat, marked, answered, contracts]);

  // The notice log: the page's history of notices, read again when the page shows, after each change of the notices
  // showing, and after each Seen.
  const noticeLog = useNoticeLog(clients, seenWindow ?? window, seenClock ?? realClock, notices);

  // markNoticeSeen sends the notice's key as seen, then lists the log again, so its unread count follows the report. A
  // failed report is dropped; the notice stays in the stack until it ends.
  const markNoticeSeen = (notice: NoticeFields) => {
    void clients.notices.seen({ key: notice.key }).catch(() => undefined).then(noticeLog.relist);
  };

  // markAllNoticesRead sends ReadAll, then lists the log again, so the centre's rows and the unread count follow it. A
  // failed report is dropped.
  const markAllNoticesRead = () => {
    void clients.notices.readAll({}).catch(() => undefined).then(noticeLog.relist);
  };

  // noticeAgeOf is a notice's age from the log's newest row under its key, or "" when the log holds none.
  const noticeAgeOf = (notice: InAppNotice) => {
    const row = noticeLog.rows.find((listed) => noticeKey(listed.key) === noticeKey(notice.key));
    return row === undefined ? "" : noticeAge(row.time, now);
  };

  // openNotice counts a notice seen, then lands on what it names. An epic moves the cursor to its entry, wherever the
  // list holds it, and lands its page on the block the notice names. A pane or a session moves the cursor to its row or
  // step, when the list shows it. A failed report is dropped; the notice stays in the stack until it ends.
  const openNotice = navigated((notice: NoticeFields) => {
    markNoticeSeen(notice);
    const epic = notice.key?.epic ?? "";
    if (epic !== "") {
      if (listsEpic(marked, epic)) {
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

  // toggleInbox collapses the inbox to its rail or expands it, closing any flyout: the inbox foot's control, the rail's
  // mark and b do this. Under 760px, where the stacked layout draws the inbox, it does nothing.
  const toggleInbox = () => {
    if (wide) {
      setInboxCollapsed((value) => !value);
      setFlyout(null);
    }
  };
  // expandInbox expands a collapsed inbox, closing any flyout.
  const expandInbox = () => {
    setInboxCollapsed(false);
    setFlyout(null);
  };
  // hoverTile opens the flyout of the project whose tile is under the pointer, taking over a flyout a key opened.
  // hoverOff closes a flyout the pointer opened, and leaves one a key opened.
  const hoverTile = (key: string) =>
    setFlyout((previous) => (previous?.key === key && previous.by === "hover" ? previous : { key, by: "hover" }));
  const hoverOff = () => setFlyout((previous) => (previous?.by === "hover" ? null : previous));
  // openFlyout opens the flyout by key on the tile of the project that holds the open page, else on the first tile.
  const openFlyout = () => {
    const tile = tiles.find((candidate) => candidate.cur) ?? tiles[0];
    if (tile !== undefined) {
      setFlyout({ key: tile.key, by: "key" });
    }
  };
  // moveFlyout moves the open flyout by key to the tile step below or above its own, held at the first and the last.
  const moveFlyout = (step: 1 | -1) => {
    const at = tiles.findIndex((tile) => tile.key === flyoutModel?.key);
    if (at >= 0) {
      setFlyout({ key: tiles[Math.max(0, Math.min(tiles.length - 1, at + step))].key, by: "key" });
    }
  };
  // openFlyoutItem opens a flyout's row as the tree's press on its item does, and a conversation's row into its reply
  // box, as a click on the row does. The inbox stays collapsed. The rail's flyouts are always the inbox's, so in the
  // Archived view the inbox view comes back first, and the row opens once it has drawn. That open is a passive effect
  // declared after the held cursor's effect, as the address's pending open is, so its cursor move wins in its commit.
  const pendingFlyoutItem = useRef<FlyoutRow | null>(null);
  const openFlyoutItem = (item: FlyoutRow) => {
    setFlyout(null);
    if (view !== "inbox") {
      pendingFlyoutItem.current = item;
      openView("inbox");
      return;
    }
    openProjectItem(item.key);
    if (item.kind === "row") {
      setBoxOpened({ column: "cursor", key: item.key });
    }
  };
  useEffect(() => {
    const item = pendingFlyoutItem.current;
    if (item !== null && view === "inbox") {
      pendingFlyoutItem.current = null;
      openFlyoutItem(item);
    }
  }, [view]);
  // openLineage is a press on a mark of the rail's lineage. A design's mark opens its card, as its tab does. An epic's or a
  // contract's mark opens it as a flyout row does; openFlyoutItem reads only the item's key and kind, so the rest of the row
  // is left empty.
  const openLineage = (key: string) => {
    if (key.startsWith("design:")) {
      navigated(openDesign)(key.slice("design:".length));
      return;
    }
    openFlyoutItem({ kind: "hung", key, name: "", ask: "", changed: 0 });
  };
  // goToProject is a click on a project's tile, and ⏎ in its flyout: it opens the flyout's cursor row, which is the
  // project's oldest row that waits on you, else its first new row, and with neither it opens the project's page.
  const goToProject = (key: string) => {
    const project = railTree.find((candidate) => candidate.key === key);
    if (project === undefined) {
      return;
    }
    const first = flyoutOf(project, now).rows[0];
    if (first === undefined) {
      openProject(key);
    } else {
      openFlyoutItem(first);
    }
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
  // question asks while the row waits and its pane or session is not put off. Only the middle column's head draws the
  // work links, since they act on the middle conversation.
  // bandContractsOf gives the band's contract lines for the contracts a session proposed that wait on the owner: each
  // with the words of what is left to settle, and whether its irreversible section is read.
  const bandContractsOf = (session: string): ContractBandLine[] =>
    session === ""
      ? []
      : contracts
          .filter((candidate) => candidate.state === "proposed" && candidate.conversation === session)
          .map((candidate) => ({
            id: candidate.id,
            slug: contractSlug(candidate.id),
            text: bandText(
              leftOf(
                Number(candidate.openDecisions),
                candidate.care,
                contractRead.has(readKey(candidate.id, candidate.version)),
              ),
            ),
          }));
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
      contracts: middle ? bandContractsOf(workConversation(row, conversation).session) : undefined,
      onOpenContract: navigated(openContractPanel),
      hereContract: middle && contract !== undefined ? contract.id : undefined,
      // While a card is held the card is the work, so the head names no work link. Under 760px the sheet under the head
      // holds the work, and its link is the one lit.
      workLinks:
        middle && heldThing === null
          ? {
              items: workLinksOf(workConversation(row, conversation).session, designs, railEpics, contracts),
              onOpen: openWorkLink,
            }
          : undefined,
      sheetKey: middle && sheetHeld && openThing !== null ? openThing.key : undefined,
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

  // focusCursorRow moves focus to the list's current row, the row the cursor rests on. A collapsed inbox holds that row in
  // the list it does not draw, so it is expanded first, and the focus waits for the layout effect after the render that
  // draws it, as a browser takes no focus on a row it does not display.
  const focusPending = useRef(false);
  useLayoutEffect(() => {
    if (focusPending.current) {
      focusPending.current = false;
      document.querySelector<HTMLElement>(".row.cur")?.focus();
    }
  });
  const focusCursorRow = () => {
    if (collapsed) {
      focusPending.current = true;
      expandInbox();
      return;
    }
    document.querySelector<HTMLElement>(".row.cur")?.focus();
  };
  // tabToShow is the design tab a design link last asked to show, by its key. The layout effect after the render that draws
  // it scrolls the tab into view and clears it, so each press shows its tab, even one that changes nothing else.
  const [tabToShow, setTabToShow] = useState("");
  useLayoutEffect(() => {
    if (tabToShow === "") {
      return;
    }
    const tab = Array.from(document.querySelectorAll<HTMLElement>(".design-tab")).find(
      (candidate) => candidate.dataset.key === tabToShow,
    );
    tab?.scrollIntoView({ block: "nearest" });
    setTabToShow("");
  }, [tabToShow]);
  // backToInbox is a row's page's Back under 760px: the inbox draws again, and the row takes the focus once the render
  // that draws the list has run.
  const backToInbox = () => {
    focusPending.current = true;
    setProjectKey(null);
    setRowOpened(false);
  };
  // closeAccounts is the accounts page's Back under 760px: the inbox draws again, as a row's page's Back does.
  const closeAccounts = navigated(() => backToInbox());

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

  // The middle conversation is the row with its session, whether it is a pane, and the background tasks its stream holds.
  // Its things are what its side panels take their tones and tasks from: the epics it steers, read as the landed seen
  // writes leave them, with the cursor's epic kept among the just ended so its tone stays while the cursor rests on it,
  // and its running lists.
  const railEpics = railEpicsOf(marked, seenEpics, epicEntry?.id ?? "");
  const railConversation: WorkConversation | null =
    middleRow === null ? null : workConversation(middleRow, cursorConversation);
  const workThings = workOf(railConversation, railEpics);
  // The epic panel is open while the cursor rests on an epic entry, its page is not closed, no contract is open, no
  // project page is shown, and the page is drawn: under 760px only once the entry has been opened. Its card takes the
  // tone of the epic among the middle conversation's things, so an epic no conversation steers takes the quiet tone.
  const panelOpen =
    pageDrawn &&
    epicEntry !== null &&
    !epicPage.closed &&
    !contractShown &&
    !designShown &&
    shownProject === null &&
    !accountsOpen;
  // The rail's tiles, and the lineage the tiles hang under them. The lineage marks open the design whose card is held, else
  // the contract whose panel is open, else the epic whose panel is open (panelOpen), and nothing while a project page is
  // shown or no panel is open.
  const lineageThing =
    design !== undefined
      ? `design:${design.id}`
      : contract !== undefined
        ? `contract:${contract.id}`
        : panelOpen && epicEntry !== null
          ? `epic:${epicEntry.id}`
          : "";
  const tiles = railTiles(railTree, shownProject ?? cursorKey, lineageThing);
  // Under 760px an open epic is the page in the conversation's place, unless its conversation is drawn and holds it as the
  // sheet under the head (sheetHeld); the page's foot bar then opens the steering conversation. At the desk the epic panel
  // lies beside the conversation, but not a worker session, which takes the conversation's place (sessionAtDesk).
  const epicPageShown = !wide && panelOpen;
  // At the desk an open worker session is the middle conversation: it draws in the middle column, and no epic panel lies
  // beside it. Its back returns to the record in the panel. A session whose step the epic's detail lacks stays in the
  // panel, where its line says so beside the panel's close.
  const sessionAtDesk =
    wide &&
    panelOpen &&
    epicEntry !== null &&
    openStep !== "" &&
    epicDetail?.steps.some((step) => step.id === openStep) === true;
  const epicBeside = panelOpen && !sessionAtDesk;
  // Under 760px an open contract is the page in the conversation's place too, unless its conversation holds it as the sheet
  // under the head (sheetHeld).
  const contractPageShown = !wide && contractShown;
  // openThing is the thing open over its conversation at any width: a design whose card is held, an epic whose panel is open,
  // or an open contract, with the key of the middle conversation's row ("" when none). At the desk it is held as a card set
  // into the conversation, as heldThing; under 760px it is the sheet under the conversation's head (sheetHeld).
  const openThing: HeldThing | null =
    design !== undefined
      ? { kind: "design", key: `design:${design.id}`, convo: middleKey }
      : contract !== undefined
        ? { kind: "contract", key: `contract:${contract.id}`, convo: middleKey }
        : epicBeside && epicEntry !== null
          ? { kind: "epic", key: `epic:${epicEntry.id}`, convo: middleKey }
          : null;
  const heldThing: HeldThing | null = wide ? openThing : null;
  // The list panel's tone comes from its row's tasks, and its rows are the middle conversation's running tasks of its kind,
  // which are empty once none runs.
  const listThing = workThings.find(
    (thing): thing is ListThing => (thing.kind === "agents" || thing.kind === "running") && thing.key === listShown,
  );
  const listTone = listThing?.tone ?? "quiet";
  const listTasks = listThing?.tasks ?? [];
  // What the panel holds while it is open: ] reopens it once it shuts. A design is held while its card is held, a contract
  // while its panel is open, an epic while its panel is open, and a list while its panel is open on its row.
  const openHeld: Held | null =
    design !== undefined
      ? { kind: "design", id: design.id }
      : contract !== undefined
        ? { kind: "contract", id: contract.id }
        : panelOpen && epicEntry !== null
          ? { kind: "epic", id: epicEntry.id }
          : listShown === null
            ? null
            : { kind: "list", open: { key: cursorRowKey, panel: listShown } };
  const heldKey =
    openHeld === null
      ? ""
      : openHeld.kind === "design"
        ? `design:${openHeld.id}`
        : openHeld.kind === "epic"
          ? `epic:${openHeld.id}`
          : openHeld.kind === "contract"
            ? `contract:${openHeld.id}`
            : `list:${openHeld.open.key}:${openHeld.open.panel}`;
  useEffect(() => {
    if (openHeld !== null) {
      setHeld(openHeld);
    }
  }, [heldKey]);
  // closePanel shuts the epic panel, from its Fold back or ]. It moves the cursor to the steerer's row, as an open,
  // when the list shows that row; with no such row in the list, it closes the epic page instead, which under 760px
  // returns to the inbox.
  const closePanel = navigated(() => {
    const target = flat.findIndex((item) => item.key === middleKey);
    if (middleRow !== null && target >= 0) {
      moveTo(target);
    } else {
      setEpicPage(closedEpicPage);
      if (!wide) {
        backToInbox();
      }
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
  // closeList shuts the list panel, from its Close or a press on its activity line. The cursor stays on the session's row,
  // so the address is the session's own.
  const closeList = navigated(() => setListPanel(null));
  // closeContract shuts the contract panel, from its Fold back or ]. The address is the
  // cursor's entry again.
  const closeContract = navigated(() => setOpenedContract(null));
  // closeDesign folds the held design card back, from its Fold back or ]. The cursor stays on the conversation's row, so the
  // address is the conversation's own.
  const closeDesign = navigated(() => setOpenedDesign(null));
  // openDesign is a press on a design's tab, its link in the title or its mark on the rail. At the desk the design's card is
  // held beside its conversation, which takes the cursor, and an open contract's panel shuts. The design's tab shows in the
  // inbox, where the tree draws it under its conversation: a collapsed inbox expands, and the tab scrolls into view once it
  // is drawn. Under 760px the design's sheet is held over its live conversation, as an epic's or a contract's is; with no live
  // conversation the page and the panels shut, so the inbox list draws with the tab in view, and no card is held.
  const openDesign = (id: string) => {
    revealKey(`design:${id}`);
    setView("inbox");
    setProjectKey(null);
    setTabToShow(`design:${id}`);
    const pressed = designs.find((candidate) => candidate.id === id);
    const conversation = pressed === undefined ? null : steererRowOf(shownRows, pressed.conversation);
    if (!wide && (conversation === null || conversation.archived || endedStates.includes(conversation.state))) {
      setRowOpened(false);
      setEpicPage(closedEpicPage);
      setOpenedContract(null);
      return;
    }
    if (collapsed) {
      expandInbox();
    }
    if (pressed === undefined) {
      return;
    }
    const conversationItem =
      conversation === null ? undefined : flat.find((candidate) => candidate.key === conversation.key);
    setOpenedContract(null);
    if (conversationItem !== undefined) {
      selectItem(conversationItem);
    }
    setOpenedDesign({ id, cursor: conversationItem?.key ?? cursorKey });
    if (!wide) {
      setRowOpened(true);
    }
  };
  // openWorkLink is a press on a work link in the title row: an epic's opens its panel, a contract's opens its contract,
  // and a design's opens its card.
  const openWorkLink = (item: WorkLinkItem) => {
    if (item.kind === "epic") {
      navigated(openEpic)(item.id);
    } else if (item.kind === "contract") {
      navigated(openContractPanel)(item.id);
    } else {
      navigated(openDesign)(item.id);
    }
  };
  // openActivity is a press on an activity line above the composer: it opens the list panel of the line's kind on the
  // middle conversation's row, or shuts that panel when it is the one open, as a press on the lit line does.
  const openActivity = (kind: ListPanelKind) => {
    if (listShown === kind) {
      closeList();
      return;
    }
    navigated(openList)(middleKey, kind);
  };
  // togglePanel is ]: it folds the held card back, or shuts the open panel, or with none open reopens the thing the panel
  // held last, as long as the list still holds it.
  const togglePanel = () => {
    if (design !== undefined) {
      closeDesign();
      return;
    }
    if (contract !== undefined) {
      closeContract();
      return;
    }
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
    if (held.kind === "design") {
      if (designs.some((candidate) => candidate.id === held.id)) {
        navigated(openDesign)(held.id);
      }
      return;
    }
    if (held.kind === "contract") {
      if (contracts.some((candidate) => candidate.id === held.id)) {
        navigated(openContractPanel)(held.id);
      }
      return;
    }
    if (held.kind === "epic") {
      if (!listsEpic(marked, held.id)) {
        return;
      }
      // An epic shut with no conversation to go to keeps the cursor, so its page is opened again where it is: under
      // 760px drawn alone.
      if (cursorKey === `epic:${held.id}`) {
        navigated(() => (wide ? setEpicPage(openEpicPage) : openPageAlone()))();
      } else {
        navigated(openEpic)(held.id);
      }
      return;
    }
    navigated(openList)(held.open.key, held.open.panel);
  };

  // Under 760px the notice centre is a page of its own, which draws alone: the list, the reader and the pages under it
  // hide while it is open, and Back shows them as they were.
  const noticesPage = !wide && noticesOpen;
  // sheetHeld is whether the phone's sheet holds the open thing under its conversation's head (D5, D9): under 760px an open
  // epic, contract or design whose conversation is drawn, and no talk raised over the record. With no conversation the thing
  // draws its page alone, as before.
  const sheetHeld =
    !wide &&
    pageDrawn &&
    !noticesPage &&
    !talking &&
    shownProject === null &&
    !accountsOpen &&
    openThing !== null &&
    middleRow !== null;
  // The list hides under 760px while the opened row's, step's, epic's, contract's or project's page draws alone, and
  // while the notice centre does, or while the sheet holds the open thing under its conversation.
  const listHides =
    !wide &&
    (noticesPage ||
      shownProject !== null ||
      accountsOpen ||
      (rowOpened && (cursorRow !== null || cursorStep !== null)) ||
      panelOpen ||
      contractPageShown ||
      sheetHeld);
  // The list is on screen where it is drawn: beside the page at the desk unless the inbox is collapsed, and under 760px
  // unless a page draws alone over it. A project page or the notice centre covers the cursor row unless the list is on
  // screen, and A, x and e do not act on a covered row.
  const listOnScreen = wide ? !collapsed : !listHides;
  const rowCovered = (shownProject !== null || noticesOpen || accountsOpen) && !listOnScreen;

  // The keys read this render's state and call this render's actions, so the document listener runs the latest context.
  const keyContext: KeyContext = {
    pickerOpen,
    openMenu,
    helpOpen,
    collapsed,
    flyoutOpen: flyoutModel !== null,
    rowCovered,
    projectShown: shownProject !== null,
    noticesOpen,
    closeNotices: () => setNoticesOpen(false),
    index,
    flat,
    waitKeys,
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
    waitTo: keyMoved(waitTo),
    nextUnseen: keyMoved(nextUnseen),
    writeSeen,
    escapeRow,
    replyBoxTarget,
    focusCursorRow,
    backOneCrumb: navigated(backOneCrumb),
    // Enter opens the cursor's page, which shuts a project page open in its place.
    openCursorPage: () => {
      setProjectKey(null);
      if (cursorItem?.kind === "contract") {
        navigated(openContractPanel)(cursorItem.contract.id);
        return;
      }
      if (!wide) {
        openPageAlone();
      }
    },
    answerCursor,
    toggleOpen,
    closePin,
    toggleInbox,
    expandInbox,
    openFlyout,
    moveFlyout,
    openFlyoutRow: () => {
      if (flyoutModel !== null) {
        goToProject(flyoutModel.key);
      }
    },
    closeFlyout: () => setFlyout(null),
    toggleStrips,
    togglePanel,
    setPickerOpen,
    setMenuKey,
    setFilter: openSearchField,
    setRowOpened: navigated((opened: boolean | ((previous: boolean) => boolean)) => {
      if (opened === true) {
        setProjectKey(null);
      }
      setRowOpened(opened);
    }),
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
  // the epic through openEpic; a step card opens its step through openEpicStep. A move's line names a session by its
  // listed conversation's title.
  const epicCalls = {
    ...epicCallsOf(clients, navigated(openEpic), navigated(openEpicStep), now, epics),
    sessionTitle: (session: string) => steererRowOf(rows, session)?.label,
  };

  // projectSection draws the page of the project under key, as the tree holds it. A key the tree holds no project of
  // draws the line that says so, with a Back of its own, since the list is hidden under 760px while the page draws.
  const projectSection = (key: string) => {
    const project = tree.find((candidate) => candidate.key === key);
    if (project === undefined) {
      return (
        <section class="read" aria-label="project">
          <div class="head">
            <TitleRow crumbs={[{ label: "Project" }]} onBack={navigated(backToInbox)} />
          </div>
          <p class="side-note">{noProjectText}</p>
        </section>
      );
    }
    return (
      <ProjectPage
        project={project}
        now={now}
        narrow={!wide}
        onOpen={openProjectItem}
        onNewHere={openPickerIn}
        onBack={navigated(backToInbox)}
      />
    );
  };

  // framed is whether the open thing is drawn in a card or the phone's sheet, whose own close is its way out: its page then
  // has no back. A page drawn alone under 760px keeps its back.
  const framed = heldThing !== null || sheetHeld;
  // epicRootBack is the back of the epic's root page: under 760px the page keeps it, and the page a held card or sheet draws
  // has none, since Fold back and the sheet's X are its way out (D15).
  const epicRootBack = framed ? undefined : closeEpicPage;
  // epicPageSection draws the epic section over the epic's detail, or while the detail is not drawn, the title row with
  // the line its read leaves: loading, or the could-not-read line with Try again. live is whether the section takes pageRef,
  // which a copy folding away does not.
  const epicPageSection = (entry: EpicItem, detail: EpicsGetResponse | undefined, live = true) => (
    <section class={pageClasses(epicPage.move)} aria-label="epic" ref={live ? pageRef : undefined}>
      {detail !== undefined ? (
        pageNode(entry, detail)
      ) : (
        <>
          <TitleRow
            crumbs={[{ label: entry.slug }]}
            onBack={epicRootBack}
            strip={wide ? pageStrip(null, null) : undefined}
          />
          {failedEpicReads.has(entry.id) ? (
            <>
              <p class="side-note unread">{couldNotReadEpicText}</p>
              <button type="button" class="act" onClick={() => retryEpicRead(entry.id)}>
                Try again
              </button>
            </>
          ) : (
            <p class="side-note">{loadingEpicText}</p>
          )}
        </>
      )}
    </section>
  );

  // contractPage draws the contract the address opened: its title row and its record, which is live while its
  // conversation is. writerOf names a session by its row's line, or by its id when no row holds it.
  const writerOf = (session: string): string => steererRowOf(rows, session)?.label ?? session;
  const readContract = (key: string) => setContractRead((previous) => new Set(previous).add(key));
  // talkAbout is a row's control on the record: the row becomes the dock's About, and under 760px the conversation
  // rises in the sheet over the record.
  const talkAbout = (value: RowAbout) => {
    setAbout(value);
    if (!wide) {
      setTalking(true);
    }
  };
  // The held design's record is read when its card is held, and again when its listed entry changes. Until a read answers,
  // the page draws the record the design held before, or the loading line.
  const heldDesignId = design?.id ?? "";
  const heldDesignListing = design === undefined ? "" : toJsonString(DesignSchema, design);
  useEffect(() => {
    if (heldDesignId !== "") {
      void refetchDesign(heldDesignId);
    }
  }, [heldDesignId, heldDesignListing, refetchDesign]);
  // answerDesign sends the owner's answer to a question of a design, from the design's page or from a conversation's thread:
  // Accept sends accept, and the question's box its words. A refused answer rejects, so the question's thread shows the
  // refusal. An answer that lands reads the design again.
  const answerDesign = useCallback(
    async (designId: string, question: bigint, answer: QuestionAnswer): Promise<void> => {
      await clients.designs.answer(
        "words" in answer
          ? { design: designId, question, answer: answer.words }
          : { design: designId, question, accept: true },
      );
      await refetchDesign(designId);
    },
    [clients, refetchDesign],
  );
  // designCalls is what the readers draw a conversation's design items through: the design records and the answer above. It
  // is made again only when a record changes, so the readers it reaches do not draw again for a render that moves nothing.
  const designCalls = useMemo<DesignCalls>(
    () => ({ records: designRecords, answer: answerDesign }),
    [designRecords, answerDesign],
  );
  // contractDesignOf is the design whose contracts include the contract id, as its record holds it: its questions and log,
  // and the answer its questions take, which is the design page's own. It is absent when no design holds the contract, or
  // the design's record has no stage yet.
  const contractDesignOf = (contractId: string): ContractDesign | undefined => {
    const holder = designs.find((candidate) => candidate.contracts.some((listed) => listed.id === contractId));
    const record = holder === undefined ? undefined : designRecords.get(holder.id)?.get;
    if (holder === undefined || record?.stage === undefined) {
      return undefined;
    }
    return {
      questions: record.stage.questions,
      log: record.log,
      onAnswer: (question, answer) => answerDesign(holder.id, question, answer),
    };
  };
  // The design of the open contract is read when the contract opens, so the questions it carries can draw, and again when
  // its listed entry changes, as the held design's is.
  const contractDesign =
    contract === undefined
      ? undefined
      : designs.find((candidate) => candidate.contracts.some((listed) => listed.id === contract.id));
  const contractDesignId = contractDesign?.id ?? "";
  const contractDesignListing = contractDesign === undefined ? "" : toJsonString(DesignSchema, contractDesign);
  useEffect(() => {
    if (contractDesignId !== "") {
      void refetchDesign(contractDesignId);
    }
  }, [contractDesignId, contractDesignListing, refetchDesign]);
  // designPage draws the design the card holds: its record's stage strip, steelman, questions, research and log, and its
  // contracts in position order. A press on a contract holds that contract instead, when the contracts the list holds
  // include it.
  const designPage =
    design === undefined ? null : (
      <DesignPage
        design={design}
        get={designRecords.get(design.id)?.get ?? null}
        error={designRecords.get(design.id)?.error ?? ""}
        onOpenContract={(id) => {
          if (contracts.some((candidate) => candidate.id === id)) {
            navigated(openContractPanel)(id);
          }
        }}
        onAnswer={(question, answer) => answerDesign(design.id, question, answer)}
      />
    );
  const contractPage =
    contract === undefined ? null : (
      <div>
        <TitleRow
          crumbs={[{ label: contractSlug(contract.id) }, { label: "contract" }]}
          state={statePill(contract.state)}
          onBack={framed ? undefined : closeContract}
          strip={pageStrip(null, null)}
        />
        <ContractRecord
          key={contract.id}
          id={contract.id}
          clients={clients}
          writer={writerOf}
          hasEpic={listsEpic(epics, contract.id)}
          read={contractRead}
          onRead={readContract}
          live={contractLiveRow !== null}
          onAbout={talkAbout}
          aboutId={about?.id}
          onTalk={
            wide || contractLiveRow === null || contract.state !== "proposed" ? undefined : () => setTalking(true)
          }
          onOpenEpic={navigated(openEpic)}
          design={contractDesignOf(contract.id)}
        />
      </div>
    );

  // steeredBar is an epic's ended bar, which steers the epic from another conversation, and whose current steering is
  // current. A session the bar moves the epic to opens the epic's entry, and is recorded for the epic as the title row
  // records it.
  const steeredBar = (epic: Epic, current: string, lead: string) => (
    <EndedBar
      epic={epic.id}
      slug={epic.slug}
      repository={epic.repository}
      steering={current}
      rows={rows}
      lead={lead}
      calls={steerCallsOf(clients)}
      narrow={!wide}
      onSteered={(session) => {
        navigated(openEpic)(epic.id);
        setSteered({ epic: epic.id, listed: epic.steering, session });
      }}
    />
  );

  // endedBarOf is the ended bar in place of a stopped or failed headless conversation's dock, when a live epic
  // that conversation steers is listed. Its lead says the conversation ended, and the bar continues the epic.
  const endedBarOf = (row: Row) => {
    if (row.archiveKind !== "headless" || (row.state !== "stopped" && row.state !== "failed")) {
      return null;
    }
    const epic = [...epics.listed.values()].find(
      (listed) => listed.live && listed.steering === row.archiveId,
    );
    return epic === undefined ? null : steeredBar(epic, epic.steering, endedLead(epic.slug));
  };

  // unsteeredBar is the ended bar of an epic entry whose steerer has no listed row, when the epic is live: its lead
  // names the epic's steering, unsteered when it has none, else its conversation ended. An ended epic has none.
  const unsteeredBar = (detail: EpicsGetResponse | undefined) => {
    if (detail?.epic === undefined || !steersLive(detail)) {
      return null;
    }
    const lead = conversationEnded(rows, steering) ? endedLead(detail.epic.slug) : unsteeredLead(detail.epic.slug);
    return steeredBar(detail.epic, steering, lead);
  };

  // noSteererMiddle is the middle beside an epic entry whose steerer has no listed row: its ended bar, or for an epic
  // that has none the no-conversation line and no control.
  const noSteererMiddle = (detail: EpicsGetResponse | undefined) =>
    unsteeredBar(detail) ?? <p class="side-note">{noSteererText}</p>;

  // pageNode draws the page the section holds over the epic's detail: the epic page, or the contract, the phase output
  // or the worker session the page has moved to.
  const pageNode = (entry: EpicItem, detail: EpicsGetResponse) => {
    const { page } = epicPage;
    if (page.kind === "contract") {
      const crumbs = [{ label: detail.epic?.slug ?? "", onGo: toEpicPage }, { label: "contract" }];
      // The epic's contract is its record when the contracts list holds it, else the contract Epics.Get holds.
      const listed = contracts.find((candidate) => candidate.id === entry.id);
      if (listed !== undefined) {
        return (
          <div>
            <TitleRow crumbs={crumbs} onBack={back} strip={pageStrip(null, null)} />
            <ContractRecord
              key={listed.id}
              id={listed.id}
              clients={clients}
              writer={writerOf}
              hasEpic
              read={contractRead}
              onRead={readContract}
              live={false}
              design={contractDesignOf(listed.id)}
            />
          </div>
        );
      }
      if (detail.contract !== undefined) {
        return (
          <div>
            <TitleRow crumbs={crumbs} onBack={back} strip={pageStrip(null, null)} />
            <ContractBody contract={detail.contract} requirements={detail.requirements} />
          </div>
        );
      }
    }
    if (page.kind === "phase") {
      const briefStep = briefStepOf(detail, page.task);
      return (
        <PhaseOutputPage
          key={page.task}
          detail={detail}
          task={page.task}
          now={now}
          narrow={!wide}
          conversation={sessionStream !== null && sessionStream.key === briefStep?.id ? sessionShown : null}
          loadArtifact={loadArtifact}
          onBack={back}
          onOpenEpic={toEpicPage}
          onOpenTask={openPhase}
          onOpenSession={navigated((step: string) =>
            drill({ kind: "session", task: page.task, phase: page.phase, step }),
          )}
          hold={(step) => stepCalls(clients, step).hold()}
          stop={(step) => stepCalls(clients, step).stop()}
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
          detail={detail}
          step={step}
          task={page.task}
          phase={page.phase}
          onOpenEpic={toEpicPage}
          onOpenTask={openPhase}
          onBack={back}
          onOlder={() => sessionOlder.current()}
          nudge={calls.nudge}
          hold={calls.hold}
          stop={calls.stop}
          strip={pageStrip(null, null)}
        />
      );
    }
    const openSession = (target: SessionTarget) => setPending({ kind: target.kind, id: target.id });
    return (
      <EpicPage
        key={entry.id}
        detail={detail}
        landing={epicPage.landing}
        history={epicHistory.id === entry.id ? epicHistory.entries : []}
        now={now}
        loadArtifact={loadArtifact}
        commands={epicCommands(clients, entry.id, setEpicCommandLine, () => rereadEpic(entry.id))}
        numberKeys={!epicPage.closed}
        commandError={epicCommandLine}
        onOpenContract={openContract}
        onOpenSession={openSession}
        onOpenPhase={openPhase}
        onBack={epicRootBack}
        strip={pageStrip(null, null)}
        rows={rows}
        listed={[...epics.listed.values()]}
        narrow={!wide}
        onTalk={steererRow === null ? undefined : closePanel}
        foot={steererRow === null ? unsteeredBar(detail) : null}
        steer={(sheet) => (
          <EpicSteer
            key={entry.id}
            epic={entry.id}
            slug={detail.epic?.slug ?? ""}
            repository={detail.repository}
            steering={steering}
            rows={rows}
            clients={clients}
            narrow={!wide}
            sheet={sheet}
            onSteered={(session) => setSteered({ epic: entry.id, listed: listedSteering, session })}
          />
        )}
      />
    );
  };

  // The rows drawn with a mark, and the projects whose ended line's button draws one. A move into an open ended line marks
  // its row, which is drawn; a move behind a shut one marks that line, since its row is not drawn. Reduced motion draws no
  // mark, and the rows still land where the answer put them.
  const markedRows: ReadonlySet<string> = reduced ? new Set() : new Set(moving.keys());
  const markedLines: ReadonlySet<string> = reduced
    ? new Set()
    : new Set(
        [...moving.values()]
          .filter((move) => move.to.line === "ended" && !endedOpen.has(move.to.project))
          .map((move) => move.to.project),
      );

  // drawn is the motion the layout draws now. Under reduced motion none is drawn, and each change is a plain swap to the state after.
  const drawn = reduced ? noMotion : motion;
  // renderConvo draws a conversation of the tree: its row, and what hangs under it, with its fold of ended epics, which its key
  // holds open or shut. The thing held now marks its conversation and carries the move; the thing being let go still marks its
  // conversation, as leave, until its tab and necks have faded (D16).
  const renderConvo = (convo: Convo) => {
    const fading = drawn.tree !== null && drawn.tree.convo === convo.key;
    return (
      <ConvoRow
        convo={convo}
        now={now}
        cursorKey={cursorKey}
        endedOpen={convoEndedOpen.has(convo.key)}
        onEnded={() => setConvoEndedOpen((previous) => withMember(previous, convo.key, !previous.has(convo.key)))}
        renderRow={(row) => (
          <InboxRow
            key={row.key}
            row={row}
            now={now}
            cursor={row.key === cursorRowKey}
            moving={markedRows.has(row.key)}
            more={rowMore(row)}
            onOpen={() => {
              selectItem(row);
              setRowOpened(true);
              setBoxOpened({ column: "cursor", key: row.key });
            }}
            onSeen={() => {
              selectItem(row);
              void writeSeen(row);
            }}
          />
        )}
        onSelect={selectKey}
        held={fading ? drawn.tree : heldThing?.convo === convo.key ? heldThing : null}
        move={fading ? "leave" : drawn.move}
      />
    );
  };

  // notes are the error lines the list shows above its rows, in the order they were set, each led by the warning.
  const notes = [seenError, ...escapeErrors, archiveError]
    .filter((line) => line !== "")
    .map((line) => (
      <p class="inote error" role="alert" key={line}>
        <Icon name="warn" />
        <span>{line}</span>
      </p>
    ));

  // sheeted is whether the phone's sheet holds the middle conversation over the record: under 760px, once talk raised
  // it.
  const sheeted = talking && !wide;
  // Under 760px one page draws at a time. The reader and the epic page draw only once an entry has been opened, so the
  // inbox is the home page until then. The list hides while the opened row's, step's, epic's, contract's or project's
  // page draws alone, and an open epic's or contract's page takes the conversation's place, unless the sheet holds the
  // conversation over the contract. Where the sheet holds the open thing, the conversation's head stays and the sheet
  // takes its reader's place under it. A shown project page takes the page area, so no reader draws. At 760px and wider the
  // list, the conversation and the panel sit abreast.
  const readerRow =
    pageDrawn &&
    !noticesPage &&
    (sheetHeld || (!epicPageShown && !(contractPageShown && !sheeted))) &&
    shownProject === null &&
    !accountsOpen
      ? middleRow
      : null;
  const readerStep = pageDrawn && !noticesPage && shownProject === null && !accountsOpen ? cursorStep : null;
  // The design records a drawn conversation needs: each design its items name is read once, and read again when a new design
  // item for it arrives, since the count of its items moves then. A conversation that is not drawn names none.
  const drawnDesigns = useMemo(
    () => designCountsOf([readerRow === null ? null : cursorConversation, pinnedConversation]),
    [readerRow, cursorConversation, pinnedConversation],
  );
  const readDesigns = useRef(new Map<string, number>());
  useEffect(() => {
    for (const [id, count] of drawnDesigns) {
      if (readDesigns.current.get(id) !== count) {
        readDesigns.current.set(id, count);
        void refetchDesign(id);
      }
    }
  }, [drawnDesigns, refetchDesign]);
  // contractCalls are the middle reader's contract calls: its contract lines draw the contracts the list holds, and
  // open them.
  const contractCalls: ContractCalls = { contracts, onOpen: navigated(openContractPanel) };
  // activityLines are what runs under the middle conversation, drawn above its composer while its reader is drawn.
  const activityLines = activityOf(readerRow === null ? null : railConversation);
  // The held card's title is the design's title, the epic's slug or the contract's slug, its body the page the card or panel
  // drew, and its Fold back is closeDesign, closeContract or closePanel, which leave the conversation alone with the cursor on
  // its row. An epic's gauge is the one its tree row draws, and a design's or a contract's mark needs none.
  const heldGauge =
    heldThing?.kind === "epic" && epicEntry !== null && epicDetail !== undefined
      ? epicGaugeOf(epics.listed.get(epicEntry.id) ?? epicDetail.epic, epicDetail, now)
      : null;
  // columnHolds is whether the middle column draws, and so holds the card; with no row or step to draw, the column is hidden
  // and the card sits in the no-conversation middle instead.
  const columnHolds = readerRow !== null || readerStep !== null;
  const heldTitle =
    design !== undefined ? design.title : contract !== undefined ? contractSlug(contract.id) : (epicEntry?.slug ?? "");
  // heldBodyOf draws the page the card or the sheet holds: the design's card, the contract's page or the epic's section. live is
  // passed on to the epic's section, as epicPageSection takes it.
  const heldBodyOf = (live: boolean): JSX.Element | null =>
    design !== undefined
      ? designPage
      : contract !== undefined
        ? contractPage
        : epicEntry !== null
          ? epicPageSection(epicEntry, epicDetail, live)
          : null;
  // closeHeld is what the card's Fold back and the sheet's X do: they close the thing and leave the conversation alone.
  const closeHeld = design !== undefined ? closeDesign : contract !== undefined ? closeContract : closePanel;
  // shownHeld is the thing the card or the sheet holds on this width: the desk's held thing, or the phone's open thing while its
  // sheet holds it.
  const shownHeld: HeldThing | null = wide ? heldThing : sheetHeld ? openThing : null;

  // The motion of the held layout (D16). The commit that changes the held thing turns what the last commit drew into a leaving
  // card or tab, and reads the custom properties the move runs on: the chat's distance to its new place, and the aim of the open
  // row. That commit sets next, and the commit after it sets move, so a move that repeats the last one still starts. Each part
  // is left as the animation of the element that carries it ends.
  useLayoutEffect(() => {
    const panes = panesRef.current;
    const place = deskPlace(panes);
    const before = shownDesk.current;
    shownDesk.current = {
      parts: shownHeld === null ? null : { thing: shownHeld, title: heldTitle, gauge: heldGauge, body: heldBodyOf },
      sheet: !wide,
      chatLeft: place.chatLeft,
      aim: place.aim,
    };
    if (before === null) {
      return;
    }
    const change = motionOf(before.parts?.thing ?? null, shownHeld, wide);
    if (change === "") {
      return;
    }
    if (reduced) {
      setMotion(noMotion);
      return;
    }
    if (before.chatLeft !== null && place.chatLeft !== null) {
      panes?.style.setProperty("--held-dx", `${before.chatLeft - place.chatLeft}px`);
    }
    if (place.aim !== null) {
      panes?.style.setProperty("--held-aim", `${place.aim}px`);
    }
    const leaves = change === "switch" || change === "other" || change === "close";
    setMotion({
      move: "",
      next: change,
      card: leaves && before.parts !== null ? { ...before.parts, aim: before.aim, sheet: before.sheet } : null,
      tree: wide && (change === "other" || change === "close") ? (before.parts?.thing ?? null) : null,
    });
  });
  useLayoutEffect(() => {
    if (motion.next !== "") {
      setMotion((current) => ({ ...current, move: current.next, next: "" }));
    }
  }, [motion]);
  // A leaving card or tab stays only while an animation runs on it, to be taken out at its animationend. One that runs none has
  // no animationend to wait for, so it is taken out at once, rather than staying for ever.
  useLayoutEffect(() => {
    const panes = panesRef.current;
    if (motion.card !== null && !animating(panes, leavingCardSelector)) {
      setMotion((current) => ({ ...current, card: null }));
    }
    if (motion.tree !== null && !animating(panes, leavingTreeSelector)) {
      setMotion((current) => ({ ...current, tree: null }));
    }
  }, [motion]);
  // A leaving card keeps the aim it folds to, its own row's, where the panes hold the aim of the open row.
  useLayoutEffect(() => {
    if (motion.card !== null && !motion.card.sheet && motion.card.aim !== null) {
      panesRef.current
        ?.querySelector<HTMLElement>('.held-card[data-move="leave"]')
        ?.style.setProperty("--held-aim", `${motion.card.aim}px`);
    }
  }, [motion.card]);
  // endMove runs on each animationend the panes hear. The element that carries a move clears its data-move when its own
  // animation ends, and a leaving card or sheet is taken out of the state once its own animation ends. The tab's ground
  // fades out before its necks have drawn back (D20), so the leaving tree is taken out at the animationend of a leaving neck,
  // or at the tab's, when no neck is still drawing back.
  const endMove = (event: AnimationEvent) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    // The column's chat and head crumbs animate on their own, so the animationend that bubbles up from them ends the column's move.
    const carrier = target.matches(columnFadeSelector) ? target.closest(columnMarkSelector) : target;
    const carried = carrier?.getAttribute("data-move") ?? null;
    if (carrier === null || carried === null) {
      return;
    }
    if (carried !== "leave") {
      carrier.removeAttribute("data-move");
    } else if (carrier.matches(".held-card, .held-sheet")) {
      setMotion((current) => (current.card === null ? current : { ...current, card: null }));
    } else if (carrier.matches(leavingNeckSelector) || !animating(panesRef.current, leavingNeckSelector)) {
      setMotion((current) => (current.tree === null ? current : { ...current, tree: null }));
    }
  };
  // The leaving copy of a card or sheet takes no press: it is folding away, and pointer-events are off while it is.
  const leavingPress = () => undefined;
  const leaving = drawn.card;
  const leavingCard =
    leaving !== null && !leaving.sheet ? (
      <HeldCard thing={leaving.thing} title={leaving.title} move="leave" gauge={leaving.gauge} onFoldBack={leavingPress}>
        {leaving.body(false)}
      </HeldCard>
    ) : null;
  const leavingSheet =
    leaving !== null && leaving.sheet ? (
      <HeldSheet thing={leaving.thing} title={leaving.title} move="leave" gauge={leaving.gauge} onClose={leavingPress}>
        {leaving.body(false)}
      </HeldSheet>
    ) : null;
  // heldCard is the held thing's card at the desk, carrying the move. The card folding away comes before it in deskCards, so the
  // held card keeps its place among the children of the column.
  const heldCard =
    heldThing === null ? null : (
      <HeldCard thing={heldThing} title={heldTitle} move={drawn.move} gauge={heldGauge} onFoldBack={closeHeld}>
        {heldBodyOf(true)}
      </HeldCard>
    );
  // sheet is the thing the phone's sheet holds under its conversation's head, when sheetHeld says it does. Its X is closeHeld.
  const heldSheet =
    sheetHeld && openThing !== null ? (
      <HeldSheet thing={openThing} title={heldTitle} move={drawn.move} gauge={heldGauge} onClose={closeHeld}>
        {heldBodyOf(true)}
      </HeldSheet>
    ) : null;
  const deskCards = leavingCard === null && heldCard === null ? undefined : (
    <>
      {leavingCard}
      {heldCard}
    </>
  );
  const sheetCards = leavingSheet === null && heldSheet === null ? undefined : (
    <>
      {leavingSheet}
      {heldSheet}
    </>
  );
  const middleColumn = (
    <ReaderColumn
      column="cursor"
      bare={sheeted}
      clients={clients}
      now={now}
      row={readerRow}
      conversation={cursorConversation}
      rows={visited}
      interrupts={interrupts}
      pageStrip={pageStrip}
      head={(row, conversation) => headOf(row, conversation, true)}
      narrow={!wide}
      hidden={headHidden}
      onHide={hideHeader}
      onShow={showHeader}
      onOpenEpic={navigated(openEpic)}
      onBack={navigated(wide ? focusCursorRow : backToInbox)}
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
      contractCalls={contractCalls}
      about={about}
      onClearAbout={() => setAbout(null)}
      addSending={addSending}
      retractSending={retractSending}
      open={open}
      onToggleOpen={toggleOpen}
      ask={askCalls}
      onInterrupt={(row) =>
        void escapeRow({ row, column: "cursor", state: headState(row, cursorConversation) }, null)
      }
      interrupt={interruptRow}
      step={readerStep}
      steps={visitedSteps}
      stepInputs={stepInputs}
      ended={endedBarOf}
      activity={<ActivityLines lines={activityLines} now={now} open={listShown} onOpen={openActivity} />}
      held={columnHolds ? deskCards : undefined}
      holds={heldThing !== null}
      sheet={sheetCards}
      move={wide ? drawn.move : ""}
    />
  );
  // railed is whether the inbox draws as its rail: when the owner collapsed it at the desk, or while a card is held between 760px
  // and 1250px, where the card takes the room the inbox's list would. Fold back draws the inbox as it was.
  const railed = collapsed || (heldThing !== null && overlay);

  return (
    <main class="app">
      <div
        ref={panesRef}
        class={`panes${pinned !== null ? " split" : ""}${overlay ? " overlay" : ""}`}
        data-inbox={railed ? "collapsed" : "expanded"}
        data-side={heldThing !== null ? "held" : listShown !== null ? "panel" : "rail"}
        onAnimationEnd={endMove}
      >
        {railed && (
          <InboxRail
            tiles={tiles}
            move={drawn.move}
            flyout={flyoutModel}
            now={now}
            onMark={expandInbox}
            onTile={goToProject}
            onHover={hoverTile}
            onHoverOff={hoverOff}
            onFlyoutRow={openFlyoutItem}
            onFlyoutPage={openProject}
            onLineage={openLineage}
            onNew={() => setPickerOpen(true)}
            onSearch={() => {
              expandInbox();
              openSearchField("");
            }}
            unread={noticeLog.unread}
            onNotices={() => setNoticesOpen(true)}
            onKeys={() => setHelpOpen(true)}
          />
        )}
        {notices.length > 0 && (
          <NoticeStack
            notices={notices}
            head={(notice) => noticeHead(notice, tree, shownRows, now)}
            age={noticeAgeOf}
            act={(notice) => noticeAct(notice, shownRows)}
            bringBackRow={(notice) => bringBackRowOf(notice, shownRows)}
            onOpen={(notice) => {
              setNoticesOpen(false);
              openNotice(notice);
            }}
            onBringBack={(row) =>
              actions.bringBack(clients, {
                kind: "pane",
                id: row.id,
                questionHash: row.questionHash,
                changed: row.changed,
              })
            }
            onRead={markNoticeSeen}
            onMore={() => setNoticesOpen(true)}
          />
        )}
        {noticesOpen && (
          <NoticeCentre
            rows={noticeLog.rows}
            failed={noticeLog.failed}
            head={(row) => noticeHead(row, tree, shownRows, now)}
            now={now}
            narrow={!wide}
            onOpen={(row) => {
              setNoticesOpen(false);
              openNotice(row);
            }}
            onReadAll={markAllNoticesRead}
            onClose={() => setNoticesOpen(false)}
          />
        )}
        <div class="list" hidden={listHides}>
          <Inbox
            view={view}
            state={!answered ? "loading" : listFailed ? "error" : "ready"}
            projects={projects}
            waitingOf={waitingOf}
            counts={counts}
            narrow={view === "inbox" ? narrowBy : ""}
            onNarrow={setNarrowBy}
            search={searchText}
            onSearch={openSearchField}
            searchRef={inputRef}
            folded={folded}
            onFold={(key) => setFolded((previous) => withMember(previous, key, !previous.has(key)))}
            endedOpen={endedOpen}
            onEnded={(key) => setEndedOpen((previous) => withMember(previous, key, !previous.has(key)))}
            renderConvo={renderConvo}
            archivedCount={archivedCount}
            onView={openView}
            onRetry={onRetry}
            onNew={() => setPickerOpen(true)}
            unread={noticeLog.unread}
            onNotices={() => setNoticesOpen(true)}
            onKeys={() => setHelpOpen(true)}
            onOpenProject={openProject}
            onCollapse={wide ? toggleInbox : undefined}
            onAccounts={openAccounts}
            moving={markedLines}
            notes={notes}
          />
        </div>
        {shownProject !== null && !noticesPage && projectSection(shownProject)}
        {accountsOpen && !noticesPage && (
          <section class="read accounts-read" aria-label="Accounts and routes">
            <AccountsPage clients={clients} narrow={!wide} onBack={closeAccounts} />
          </section>
        )}
        <Sheet open={sheeted} title={contractLiveRow?.label ?? ""} onClose={() => setTalking(false)}>
          {sessionAtDesk ? (
            <section class={`read ${pageClasses(epicPage.move)}`} aria-label="session" ref={pageRef}>
              {pageNode(epicEntry, epicDetail)}
            </section>
          ) : (
            withDesignCalls(designCalls, middleColumn)
          )}
        </Sheet>
        {pageDrawn &&
          !noticesPage &&
          shownProject === null &&
          !accountsOpen &&
          epicEntry !== null &&
          !contractShown &&
          !designShown &&
          steererRow === null &&
          !epicPageShown &&
          !sessionAtDesk && (
            <section class="read" aria-label="no conversation">
              {noSteererMiddle(epicDetail)}
              {!columnHolds && deskCards}
            </section>
          )}
        {contractShown && contractLiveRow === null && !contractPageShown && (
          <section class="read" aria-label="no conversation">{!columnHolds && deskCards}</section>
        )}
        {designShown && designLiveRow === null && (
          <section class="read" aria-label="no conversation">{!columnHolds && deskCards}</section>
        )}
        {withDesignCalls(
          designCalls,
          <ReaderColumn
            column="pinned"
            clients={clients}
            now={now}
            row={pinned}
            conversation={pinnedConversation}
            rows={pinnedRows}
            interrupts={interrupts}
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
          />,
        )}
        {epicEntry !== null && panelOpen && !wide && !noticesPage && !sheetHeld && (
          <div class="epic-read">{epicPageSection(epicEntry, epicDetail)}</div>
        )}
        {contract !== undefined && !wide && !noticesPage && !sheetHeld && <div class="epic-read">{contractPage}</div>}
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
      </div>
      {toast !== null && (toast.kind === "moved" || !wide) && (
        <Toast
          text={toast.kind === "moved" ? moveNotice(toast.moves) : `Archived “${toast.row.label}”.`}
          actions={
            toast.kind === "moved"
              ? [{ label: "Show", onAction: () => showMove(toast.moves[0]) }, undoAction]
              : [undoAction]
          }
        />
      )}
      {pickerOpen && (
        <NewPanel
          clients={clients}
          rows={rows}
          sheet={!wide}
          dir={pickerDir}
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

// newestEnding orders ended epics newest ending first.
function newestEnding(a: EpicItem, b: EpicItem): number {
  return b.phaseAt - a.phaseAt;
}

// endingKey names one ending of an ended epic: its line's key and the time it ended. A later ending has a new key.
function endingKey(epic: EpicItem): string {
  return `${epic.key}@${epic.phaseAt}`;
}

// railEpicsOf is the epics view the rail reads: each just-ended epic whose ending a seen write landed for moves to the
// ended earlier, as the next poll will read it, and the epic under the cursor stays among the just ended, or joins them
// from the ended earlier, while the cursor rests on it. Both lists stay newest ending first.
function railEpicsOf(view: EpicsView, written: Set<string>, cursorEpic: string): EpicsView {
  const readSeen = (epic: EpicItem) => epic.id !== cursorEpic && written.has(endingKey(epic));
  const kept = (epic: EpicItem) => epic.id === cursorEpic;
  return {
    ...view,
    justEnded: [...view.justEnded.filter((epic) => !readSeen(epic)), ...view.endedEarlier.filter(kept)].sort(newestEnding),
    endedEarlier: [...view.endedEarlier.filter((epic) => !kept(epic)), ...view.justEnded.filter(readSeen)].sort(
      newestEnding,
    ),
  };
}

// unseenEnding says whether an epic's ending reads unseen: the epic is listed as not live, is not archived, was seen
// before it ended, and no landed Epics.Seen covers this ending.
function unseenEnding(epic: EpicItem, listed: Epic | undefined, written: Set<string>): boolean {
  return (
    listed !== undefined &&
    !listed.live &&
    !epic.archived &&
    Number(listed.seen) < epic.phaseAt &&
    !written.has(endingKey(epic))
  );
}

// readAsSeen clears a row's unseen mark once its seen write succeeded, until its changed time moves past the one it had.
function readAsSeen(row: Row, written: Map<string, number>): Row {
  const writtenAt = written.get(row.key);
  return row.unseen && writtenAt !== undefined && row.changed <= writtenAt ? { ...row, unseen: false } : row;
}

// HeldParts is what a held card or sheet draws, as a render drew it: the thing, its title and gauge, and its body.
interface HeldParts {
  thing: HeldThing;
  title: string;
  gauge: GaugeShare | null;
  // body draws the card's page. A copy that folds away is drawn with live false, which takes no pageRef.
  body: (live: boolean) => JSX.Element | null;
}

// LeavingCard is a card or sheet folding away: its parts as they last drew, the aim its fold runs to, and whether it is the
// phone's sheet.
interface LeavingCard extends HeldParts {
  aim: number | null;
  sheet: boolean;
}

// HeldMotion is the motion the held layout draws (D16). move is what the held thing, its tab and its necks carry. next is the
// move the commit after the one that clears move draws, so a move that repeats the last one still starts its animations. card
// is the card folding away and tree the held thing whose tab and necks fade away; each stays drawn until its own animationend.
interface HeldMotion {
  move: HeldMove;
  next: HeldMove;
  card: LeavingCard | null;
  tree: HeldThing | null;
}

const noMotion: HeldMotion = { move: "", next: "", card: null, tree: null };

// DeskShown is what the held layout drew at the last commit: the parts of the held card or sheet, whether it was the phone's
// sheet, and where the cursor's column and the open row stood.
interface DeskShown {
  parts: HeldParts | null;
  sheet: boolean;
  chatLeft: number | null;
  aim: number | null;
}

// motionOf is the move a change of the held thing draws, from before to after. At the desk every change draws its move. On the
// phone only the sheet's opening and closing draw one: the chat does not move, and a sheet that changes to another thing is a
// plain swap.
function motionOf(before: HeldThing | null, after: HeldThing | null, wide: boolean): HeldMove {
  const move = heldMove(before, after);
  return wide || move === "open" || move === "close" ? move : "";
}

// offsetWithin is the top and left of element within root, summed over the offsets between them. offsetTop and offsetLeft leave
// out the transforms a move is running, so a place read during a move is the one the layout gives. It is null when root is not
// among element's offset parents.
function offsetWithin(element: HTMLElement, root: HTMLElement): { top: number; left: number } | null {
  let top = 0;
  let left = 0;
  let at: HTMLElement | null = element;
  while (at !== null && at !== root) {
    top += at.offsetTop;
    left += at.offsetLeft;
    at = (at.offsetParent as HTMLElement | null) ?? null;
  }
  return at === root ? { top, left } : null;
}

// deskPlace reads where the held layout stands within panes: the left edge of the cursor's chat, which is its reader, and the aim
// of the held card, the middle of the open row from the card's top, which is --held-aim. Both come from offsetTop and offsetLeft.
function deskPlace(panes: HTMLElement | null): { chatLeft: number | null; aim: number | null } {
  const reader = panes?.querySelector<HTMLElement>('section[aria-label="reader"] > .reader') ?? null;
  const card = panes?.querySelector<HTMLElement>(".held-card") ?? null;
  const row = panes?.querySelector<HTMLElement>('[data-held="open"]') ?? null;
  const chat = panes !== null && reader !== null ? offsetWithin(reader, panes) : null;
  const cardAt = panes !== null && card !== null ? offsetWithin(card, panes) : null;
  const rowAt = panes !== null && row !== null ? offsetWithin(row, panes) : null;
  return {
    chatLeft: chat?.left ?? null,
    aim: cardAt !== null && rowAt !== null && row !== null ? rowAt.top + row.offsetHeight / 2 - cardAt.top : null,
  };
}

// leavingCardSelector and leavingTreeSelector name the card or sheet folding away, and the tab and necks fading away, while
// they carry leave.
const leavingCardSelector = '.held-card[data-move="leave"], .held-sheet[data-move="leave"]';
const leavingTreeSelector = '[data-held][data-move="leave"]';
// leavingNeckSelector names the necks of a leaving tree, the rows and marks that draw back into their rows, which are every leaving
// element but the tab, .convo-block[data-held="tab"].
const leavingNeckSelector = '[data-held][data-move="leave"]:not(.convo-block[data-held="tab"])';
// columnMarkSelector is the column that carries a move, and columnFadeSelector the parts of it whose animations run on their own,
// as style.css draws them: the chat's reader and send box, which move or fade, and the head's crumbs, which fade.
const columnMarkSelector = ".read[data-move]";
const columnFadeSelector = ".read[data-move] > .reader, .read[data-move] > .send, .read[data-move] .convo-head .crumbs";

// animating says whether an element under root that matches selector runs an animation now. An element with no Web Animations
// runs none.
function animating(root: HTMLElement | null, selector: string): boolean {
  if (root === null) {
    return false;
  }
  return [...root.querySelectorAll<HTMLElement>(selector)].some(
    (element) => typeof element.getAnimations === "function" && element.getAnimations().length > 0,
  );
}

// heldOpenKey is the key of the open entry the inbox keeps drawn while a list answer archives it, or ends it while the row
// was drawn unended. That is the cursor's conversation row or epic, drawn unarchived at the last commit, which the answer
// now archives with no local archive mark of its own; or the cursor's conversation row, drawn unended at the last commit,
// which the answer now ends. The cursor's move to another entry ends the hold, as does any other view than the inbox. It
// is "" when nothing is held.
function heldOpenKey(
  drawn: Row | EpicItem | null,
  cursorKey: string,
  view: View,
  rows: readonly Row[],
  epics: EpicsView,
  rowMarks: ReadonlyMap<string, boolean>,
  epicMarks: ReadonlyMap<string, boolean>,
): string {
  if (view !== "inbox" || drawn === null || drawn.key !== cursorKey) {
    return "";
  }
  if (drawn.kind === "epic") {
    return epics.listed.get(drawn.id)?.archived === true && !epicMarks.has(drawn.id) ? drawn.key : "";
  }
  const answered = rows.find((row) => row.key === drawn.key);
  const held = answered !== undefined && (answered.archived || (answered.ended === true && drawn.ended !== true));
  return held && !rowMarks.has(drawn.key) ? drawn.key : "";
}

// spotsOfAnswer is where a rows answer draws each conversation, in the inbox and the Archived view. It reads the answer's own
// rows and epics alone: no local archive mark and no held open entry.
function spotsOfAnswer(
  rows: readonly Row[],
  epics: EpicsView,
  contracts: readonly Contract[],
  designs: readonly Design[],
  host: string,
): Map<string, Placed> {
  const treeOf = (view: View) =>
    buildTree({ rows, epics: [...epics.listed.values()], details: epics.details, contracts, designs, host, view });
  return spotsOf(treeOf("inbox"), treeOf("archived"));
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
  const justEnded = view.justEnded.filter((item) => !archivedNow(item));
  const endedEarlier = view.endedEarlier.filter((item) => !archivedNow(item));
  const archived = [
    ...view.blocks.filter((block) => archivedNow(block.epic)).map((block) => archive(block.epic)),
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
    justEnded.push(...placed.justEnded);
    endedEarlier.push(...placed.endedEarlier);
  }
  return {
    blocks: [...blocks.filter((block) => block.full), ...blocks.filter((block) => !block.full)],
    live: blocks.length,
    justEnded: justEnded.sort(newestEnding),
    endedEarlier: endedEarlier.sort(newestEnding),
    archived,
    details: view.details,
    listed: view.listed,
  };
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

// EpicSteer is the steering command of an epic page. At the desk it is an icon in the title row that opens the steering
// menu under it. Under 760px it is the sheet that the More sheet's Steer item opens, which the page holds as sheet. It
// holds the desk menu's open state and the one steering handle the menu and its choices share, and a choice that steers
// closes the menu and reports the session.
function EpicSteer({
  epic,
  slug,
  repository,
  steering,
  rows,
  clients,
  narrow,
  sheet,
  onSteered,
}: {
  epic: string;
  slug: string;
  repository: string;
  steering: string;
  rows: Row[];
  clients: Clients;
  narrow: boolean;
  sheet: SteerSheet;
  onSteered: (session: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const steer = useSteer(epic, steerCallsOf(clients), (session) => {
    setOpen(false);
    sheet.onClose();
    onSteered(session);
  });
  const menu = (onClose: () => void) => (
    <SteerMenu slug={slug} steering={steering} repository={repository} rows={rows} steer={steer} onClose={onClose} />
  );
  if (narrow) {
    return sheet.open ? (
      <Sheet title={`Steer ${slug} from`} onClose={sheet.onClose}>
        {menu(sheet.onClose)}
      </Sheet>
    ) : null;
  }
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
    return item.kind !== "epic" && item.kind !== "contract" && session !== "" && item.id === session;
  });
}

// workConversation is what a row's work is drawn from: its session, whether it is a pane, and the background tasks its
// stream holds.
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
    case "contract":
      return null;
  }
}

// addressKey is the key the list gives the row or step an address names, which the cursor is moved to.
function addressKey(entry: Exclude<Entry, { kind: "epic" | "project" | "contract" | "accounts" }>): string {
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

// newerDetail is the epic's detail the page draws: the one whose epic has the higher revision. It is the polled detail on
// a tie, and when either detail has no epic to compare, and the one held when the other is missing.
export function newerDetail(
  polled: EpicsGetResponse | undefined,
  asked: EpicsGetResponse | undefined,
): EpicsGetResponse | undefined {
  if (polled === undefined || asked === undefined) {
    return polled ?? asked;
  }
  if (polled.epic === undefined || asked.epic === undefined) {
    return polled;
  }
  return asked.epic.revision > polled.epic.revision ? asked : polled;
}

// epicCommands binds an epic's commands to its id. A command that fails reports its did-not-land line through report,
// the command's name and the error's message as the reason; one that lands reports "". answer resolves "" when the
// answer lands, else the error's message. release and close are owner calls: each is sent once, never retried, and
// resolves its outcome rather than reporting through report. reread is the read the page makes after one.
export function epicCommands(
  clients: Clients,
  epicId: string,
  report: (line: string) => void,
  reread: () => Promise<boolean>,
): EpicCommands {
  const send = (name: string, sent: Promise<unknown>): void => {
    void sent.then(
      () => report(""),
      (error: unknown) =>
        report(`${name} did not land: ${failureReason(error) !== "" ? failureReason(error) : noReasonText}.`),
    );
  };
  return {
    pause: () => send("Pause", clients.epics.pause({ id: epicId })),
    resume: () => send("Resume", clients.epics.resume({ id: epicId })),
    stop: () => send("Stop", clients.epics.stop({ id: epicId })),
    retry: () => send("Retry", clients.epics.retry({ id: epicId })),
    answer: (question, choice, text) =>
      clients.epics.answer({ epicId, id: crypto.randomUUID(), text, question, choice }).then(
        () => "",
        (error: unknown) => (error instanceof Error && error.message !== "" ? error.message : answerFailedText),
      ),
    release: (undoNone) => sendOnce(() => clients.epics.release({ id: epicId, undoNone })),
    close: (kind, by) => sendOnce(() => clients.epics.close({ id: epicId, kind, by })),
    reread,
  };
}

function clamp(index: number, length: number): number {
  return Math.min(Math.max(index, 0), length - 1);
}

// findCurrent is the match a find bar shows: the stored one, clamped into the matches, and -1 when none match.
function findCurrent(stored: number, matches: number): number {
  return matches === 0 ? -1 : clamp(stored, matches);
}

// treeEntries lists the entries the tree draws, in the order it draws them: each project not folded, its conversations,
// then, while its ended line is open, its ended conversations. A conversation's row is an entry, then each thing hung
// under it, and then, while its fold of ended epics is open, each folded epic. A thing is an entry: an epic, which is
// followed by its running steps, or a contract.
function treeEntries(
  projects: readonly Project[],
  folded: ReadonlySet<string>,
  endedOpen: ReadonlySet<string>,
  convoEndedOpen: ReadonlySet<string>,
): Item[] {
  const entries: Item[] = [];
  const addThing = (hung: Hung) => {
    if (hung.kind === "epic") {
      entries.push(hung.block.epic, ...hung.block.steps);
    } else {
      entries.push(hung);
    }
  };
  const addConvo = (convo: Convo) => {
    if (convo.row !== null) {
      entries.push(convo.row);
    }
    convo.hung.forEach(addThing);
    if (convoEndedOpen.has(convo.key)) {
      convo.folded.forEach(addThing);
    }
  };
  for (const project of projects) {
    if (folded.has(project.key)) {
      continue;
    }
    project.convos.forEach(addConvo);
    if (endedOpen.has(project.key)) {
      project.ended.forEach(addConvo);
    }
  }
  return entries;
}

// holdsKey says whether a conversation draws the entry under key: its row, its design's tab, or a thing hung or folded under
// it, or a step of an epic hung or folded under it.
function holdsKey(convo: Convo, key: string): boolean {
  if (convo.row !== null && convo.row.key === key) {
    return true;
  }
  if ((convo.designs ?? []).some((node) => node.key === key)) {
    return true;
  }
  return [...convo.hung, ...convo.folded].some((hung) => holdsThing(hung, key));
}

// holdsThing says whether a thing is the entry under key, or one of an epic's steps.
function holdsThing(hung: Hung, key: string): boolean {
  return hung.key === key || (hung.kind === "epic" && hung.block.steps.some((step) => step.key === key));
}

// withMember is the set with key in it when member, or out of it, and previous itself when it already is.
function withMember(previous: Set<string>, key: string, member: boolean): Set<string> {
  if (previous.has(key) === member) {
    return previous;
  }
  const next = new Set(previous);
  if (member) {
    next.add(key);
  } else {
    next.delete(key);
  }
  return next;
}

// pageClasses are the classes of the epic page section: its move.
function pageClasses(move: EpicPageState["move"]): string {
  return ["page", move].filter((name) => name !== "").join(" ");
}

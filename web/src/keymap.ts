import type { Dispatch, StateUpdater } from "preact/hooks";

import type { Conversation } from "./conversation";
import type { EpicItem, StepItem } from "./epics";
import type { ContractItem } from "./tree";
import { headState } from "./readercolumn";
import type { Row } from "./rows";
import type { View } from "./router";

// An entry the tree shows and the cursor can rest on: a conversation row, an epic or step it steers, or a contract.
export type Item = Row | EpicItem | StepItem | ContractItem;

// The row an Escape acts on: the row, the column whose composer holds the key, and the row's state at the press.
export interface EscapeTarget {
  row: Row;
  column: "cursor" | "pinned";
  state: string;
}

// A reply box an opening focuses: the cursor's or the pinned column's box, by the key of the row it holds.
export interface BoxOpen {
  column: "cursor" | "pinned";
  key: string;
}

// The keys that act on the list. While the inbox is collapsed, one of them expands it and does nothing else, so the
// cursor never moves unseen. With a flyout open, j, k and the arrows move the flyout before this is read.
const inboxListKeys = new Set(["j", "k", "ArrowDown", "ArrowUp", "g", "G", "/", "v"]);

// isRow tells a row from an epic or step entry by its kind.
export function isRow(item: Item): item is Row {
  return item.kind === "pane" || item.kind === "session";
}

// KeyContext is what handleKey reads and calls: the state the keys read this render, the inputs and the pending g they
// focus or read, and one callback or setter for each action a key takes. App builds it each render.
export interface KeyContext {
  // Whether the palette is open, the entry whose ⋯ menu is open if any, and whether help is open.
  pickerOpen: boolean;
  openMenu: string | null;
  helpOpen: boolean;
  // Whether the inbox is collapsed to its rail, at 760px and wider, and whether a project's flyout is open beside it.
  collapsed: boolean;
  flyoutOpen: boolean;
  // Whether a page covers the cursor row: a project page or the notice centre is shown and the inbox list, where the
  // row is drawn, is not on screen beside or over it. A, x and e do not act on a covered row.
  rowCovered: boolean;
  // Whether a project page is shown. It covers the reader, so 1-9 and Escape's interrupt do nothing while it is.
  projectShown: boolean;
  // Whether the notice centre is open, and the closer that shuts it. The centre covers the reader like a project page,
  // and while it is open Escape closes it and does nothing else.
  noticesOpen: boolean;
  closeNotices: () => void;
  // The cursor's index in the flat list, and the flat list, which G reads.
  index: number;
  flat: Item[];
  // The keys of the counted things, oldest first, which w reads, and the view A reads: A archives in the inbox and
  // unarchives in Archived.
  waitKeys: string[];
  view: View;
  cursorItem: Item | null;
  cursorRow: Row | null;
  cursorRowKey: string;
  cursorStep: StepItem | null;
  cursorStepKey: string;
  // The conversation the cursor's row shows, which headState reads with the cursor's row.
  cursorConversation: Conversation | null;
  // The row the pinned column shows.
  pinned: Row | null;
  // The filter input, the composer and step reply boxes by row or step key, and the g that a key before this one left
  // pending.
  inputRef: { current: HTMLInputElement | null };
  composerInputs: { current: Map<string, HTMLTextAreaElement> };
  stepInputs: { current: Map<string, HTMLTextAreaElement> };
  pendingG: { current: boolean };
  moveTo: (target: number) => void;
  // waitTo moves the cursor to the entry under key, revealing the entry first when a folded project or the ended line
  // hides it.
  waitTo: (key: string) => void;
  nextUnseen: () => void;
  writeSeen: (row: Row) => Promise<void>;
  setArchive: (entry: Row | EpicItem, archived: boolean) => void;
  escapeRow: (target: EscapeTarget, box: HTMLElement | null) => Promise<void>;
  replyBoxTarget: (box: HTMLElement) => EscapeTarget | null;
  focusCursorRow: () => void;
  backOneCrumb: () => void;
  // openCursorPage opens the cursor's epic or step page as Enter on a row opens its page: under 760px it then draws
  // alone. At 760px and wider the page already stands beside the inbox, so it changes nothing there.
  openCursorPage: () => void;
  answerCursor: (choice: number) => void;
  toggleOpen: () => void;
  closePin: () => void;
  // toggleInbox collapses the inbox or expands it, closing any flyout; expandInbox expands it.
  toggleInbox: () => void;
  expandInbox: () => void;
  // openFlyout opens the flyout on the tile of the project the open page is in, else on the first tile. moveFlyout moves
  // it a tile down (1) or up (-1), held at the ends. openFlyoutRow opens the flyout's cursor row, as a click on its tile
  // does, and closeFlyout closes it.
  openFlyout: () => void;
  moveFlyout: (step: 1 | -1) => void;
  openFlyoutRow: () => void;
  closeFlyout: () => void;
  toggleStrips: () => void;
  // Shuts the open panel, or with none open reopens the thing it held last.
  togglePanel: () => void;
  setPickerOpen: Dispatch<StateUpdater<boolean>>;
  setMenuKey: Dispatch<StateUpdater<string | null>>;
  // setFilter sets the search text, which opens the search field; null shuts it.
  setFilter: (text: string | null) => void;
  setRowOpened: Dispatch<StateUpdater<boolean>>;
  setBoxOpened: Dispatch<StateUpdater<BoxOpen | null>>;
  setView: Dispatch<StateUpdater<View>>;
  setPinned: Dispatch<StateUpdater<Row | null>>;
  setPinStream: (stream: null) => void;
  setHelpOpen: Dispatch<StateUpdater<boolean>>;
}

// handleKey reads one keydown against the context App built for this render, and does the action the key names. A key
// typed in a text box is text, and only Escape and Shift+Tab leave one, so the list keeps its keys otherwise.
export function handleKey(event: KeyboardEvent, context: KeyContext): void {
  const {
    pickerOpen,
    openMenu,
    helpOpen,
    collapsed,
    flyoutOpen,
    rowCovered,
    projectShown,
    noticesOpen,
    closeNotices,
    index,
    flat,
    waitKeys,
    view,
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
    moveTo,
    waitTo,
    nextUnseen,
    writeSeen,
    setArchive,
    escapeRow,
    replyBoxTarget,
    focusCursorRow,
    backOneCrumb,
    openCursorPage,
    answerCursor,
    toggleOpen,
    closePin,
    toggleInbox,
    expandInbox,
    openFlyout,
    moveFlyout,
    openFlyoutRow,
    closeFlyout,
    toggleStrips,
    togglePanel,
    setPickerOpen,
    setMenuKey,
    setFilter,
    setRowOpened,
    setBoxOpened,
    setView,
    setPinned,
    setPinStream,
    setHelpOpen,
  } = context;
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
  // While the notice centre is open, Escape closes it before anything else, and interrupts nothing.
  if (noticesOpen && event.key === "Escape") {
    closeNotices();
    event.preventDefault();
    return;
  }
  // While a drawn entry's ⋯ menu is open, Escape closes it and does nothing else: it takes back and interrupts nothing.
  if (event.key === "Escape" && openMenu !== null) {
    setMenuKey(null);
    event.preventDefault();
    return;
  }
  const input = inputRef.current;
  if (input !== null && document.activeElement === input) {
    if (event.key === "Enter") {
      input.blur();
    } else if (event.key === "Escape") {
      setFilter(null);
      input.blur();
    }
    return;
  }
  // Keys typed in a text box are text: only Escape leaves it, and in the cursor's or the pinned row's reply box
  // Shift+Tab also leaves it, for the list. That covers the reply boxes and the answer box. Escape in the cursor's or
  // the pinned row's reply box takes its queued messages back, as Escape outside a box does; any other box is only left.
  const focused = document.activeElement;
  if (focused instanceof HTMLElement && (focused.tagName === "TEXTAREA" || focused.tagName === "INPUT")) {
    if (event.key === "Escape") {
      const target = replyBoxTarget(focused);
      if (target === null) {
        focused.blur();
      } else {
        void escapeRow(target, focused);
      }
    } else if (event.key === "Tab" && event.shiftKey && replyBoxTarget(focused) !== null) {
      // Shift+Tab takes the focus to the list's cursor row. It takes nothing back and interrupts nothing.
      event.preventDefault();
      focused.blur();
      focusCursorRow();
    }
    return;
  }
  // A flyout's keys act on it: the arrows, j and k move it a tile, Enter opens its cursor row, even while a tile or
  // another button holds the focus, and Escape closes it.
  if (flyoutOpen) {
    const action = flyoutKeys(event.key, moveFlyout, openFlyoutRow, closeFlyout);
    if (action !== null) {
      action();
      event.preventDefault();
      return;
    }
  }
  // A focused button keeps its own Enter, so New or Keys opens its panel and Enter writes no seen.
  if (event.key === "Enter" && focused instanceof HTMLElement && focused.tagName === "BUTTON") {
    return;
  }

  const afterG = pendingG.current;
  pendingG.current = false;
  // A project page or the notice centre covers the reader: 1-9 and Escape's interrupt do nothing while either is shown.
  const readerCovered = projectShown || noticesOpen;
  // While the inbox is collapsed, a list key expands it and does nothing else, so the cursor never moves unseen.
  if (collapsed && inboxListKeys.has(event.key)) {
    expandInbox();
    event.preventDefault();
    return;
  }
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
    case "v":
      setView("archived");
      break;
    case "A":
      // A pane with no orrery_id, a step and a contract have no archive kind, so the key does nothing on any of them.
      if (!rowCovered && cursorItem !== null && cursorItem.kind !== "step" && cursorItem.kind !== "contract") {
        setArchive(cursorItem, view !== "archived");
      }
      break;
    case "w": {
      // w goes to the counted thing after the cursor's entry in waitKeys, wrapping; from an entry that counts nothing,
      // to the first. Nothing counted, nothing to go to.
      if (waitKeys.length > 0) {
        const at = cursorItem === null ? -1 : waitKeys.indexOf(cursorItem.key);
        waitTo(waitKeys[(at + 1) % waitKeys.length]);
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
      if (!rowCovered && cursorRow !== null) {
        void writeSeen(cursorRow);
      }
      break;
    case "Enter":
      if (cursorRow !== null) {
        setRowOpened(true);
        void writeSeen(cursorRow);
        // Enter opens the row into its reply box, as a click on the row does; x only marks it seen.
        setBoxOpened({ column: "cursor", key: cursorRow.key });
      } else if (cursorItem?.kind === "epic" || cursorItem?.kind === "step" || cursorItem?.kind === "contract") {
        // Enter opens an epic's, a step's or a contract's page, as a click on it does.
        openCursorPage();
      }
      break;
    case "b":
      toggleInbox();
      break;
    case "p":
      // p opens a collapsed inbox's flyout; with the inbox expanded it does nothing.
      if (!collapsed) {
        return;
      }
      openFlyout();
      break;
    case "h":
    case "ArrowLeft":
      backOneCrumb();
      break;
    case "e":
      if (cursorRow !== null) {
        if (!rowCovered) {
          void writeSeen(cursorRow);
        }
        nextUnseen();
      }
      break;
    case "/":
      if (inputRef.current !== null) {
        inputRef.current.focus();
      } else {
        setFilter("");
      }
      break;
    case "i":
      if (cursorStep !== null) {
        stepInputs.current.get(cursorStepKey)?.focus();
      } else {
        composerInputs.current.get(cursorRowKey)?.focus();
      }
      break;
    // The digits do nothing while the reader is covered, and do not take the key either.
    case "1":
    case "2":
    case "3":
    case "4":
    case "5":
    case "6":
    case "7":
    case "8":
    case "9":
      if (readerCovered) {
        return;
      }
      answerCursor(Number(event.key));
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
        setBoxOpened({ column: "pinned", key: cursorRow.key });
      }
      break;
    case "m":
      toggleStrips();
      break;
    case "]":
      togglePanel();
      break;
    case "?":
      setHelpOpen((open) => !open);
      break;
    case "Escape":
      if (!helpOpen && !readerCovered && cursorRow !== null) {
        void escapeRow({ row: cursorRow, column: "cursor", state: headState(cursorRow, cursorConversation) }, null);
      }
      setHelpOpen(false);
      return;
    default:
      return;
  }
  event.preventDefault();
}

// flyoutKeys is the action a key takes on an open flyout, or null for a key the flyout leaves to the page.
function flyoutKeys(
  key: string,
  moveFlyout: (step: 1 | -1) => void,
  openFlyoutRow: () => void,
  closeFlyout: () => void,
): (() => void) | null {
  switch (key) {
    case "ArrowUp":
    case "k":
      return () => moveFlyout(-1);
    case "ArrowDown":
    case "j":
      return () => moveFlyout(1);
    case "Enter":
      return openFlyoutRow;
    case "Escape":
      return closeFlyout;
    default:
      return null;
  }
}

import type { Dispatch, StateUpdater } from "preact/hooks";

import type { Conversation } from "./conversation";
import type { EpicItem, StepItem } from "./epics";
import { stateWaiting } from "./inboxrow";
import { headState } from "./readercolumn";
import type { Row } from "./rows";

// The views the list shows, which a, p and v choose.
export type View = "attention" | "project" | "archived";

// How a hidden inbox came out over the page: by the pointer on the mark, or by a key. Only a key pop-out is tucked by
// Escape.
export type Popout = "none" | "hover" | "key";

// An entry the list shows and the cursor can rest on: a row, or an epic or step of the Epics group.
export type Item = Row | EpicItem | StepItem;

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

// The keys that act on the list. While the inbox is hidden, one of them pops it out by key and does nothing else.
const inboxListKeys = new Set(["j", "k", "ArrowDown", "ArrowUp", "g", "G", "/", "a", "p", "v"]);

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
  // Whether the inbox is hidden, and whether a key popped it out.
  inboxHidden: boolean;
  keyPopped: boolean;
  // The cursor's index in the flat list, and the flat list, which G and w read.
  index: number;
  flat: Item[];
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
  nextUnseen: () => void;
  writeSeen: (row: Row) => Promise<void>;
  setArchive: (entry: Row | EpicItem, archived: boolean) => void;
  escapeRow: (target: EscapeTarget, box: HTMLElement | null) => Promise<void>;
  replyBoxTarget: (box: HTMLElement) => EscapeTarget | null;
  focusCursorRow: () => void;
  backOneCrumb: () => void;
  answerCursor: (choice: 1 | 2 | 3) => void;
  toggleOpen: () => void;
  closePin: () => void;
  togglePin: () => void;
  toggleStrips: () => void;
  setPickerOpen: Dispatch<StateUpdater<boolean>>;
  setMenuKey: Dispatch<StateUpdater<string | null>>;
  setFilter: Dispatch<StateUpdater<string>>;
  setPopout: Dispatch<StateUpdater<Popout>>;
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
    moveTo,
    nextUnseen,
    writeSeen,
    setArchive,
    escapeRow,
    replyBoxTarget,
    focusCursorRow,
    backOneCrumb,
    answerCursor,
    toggleOpen,
    closePin,
    togglePin,
    toggleStrips,
    setPickerOpen,
    setMenuKey,
    setFilter,
    setPopout,
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
      setFilter("");
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
  // A focused button keeps its own Enter, so New or Keys opens its panel and Enter writes no seen.
  if (event.key === "Enter" && focused instanceof HTMLElement && focused.tagName === "BUTTON") {
    return;
  }

  const afterG = pendingG.current;
  pendingG.current = false;
  // While the inbox is hidden, a list key pops it out by key and does nothing else, so the cursor never moves unseen.
  if (inboxHidden && inboxListKeys.has(event.key)) {
    setPopout("key");
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
      // A pane with no orrery_id and a step have no archive kind, so the key does nothing on either.
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
      if (cursorRow !== null) {
        void writeSeen(cursorRow);
      }
      break;
    case "Enter":
      if (cursorRow !== null) {
        setRowOpened(true);
        setPopout("none");
        void writeSeen(cursorRow);
        // Enter opens the row into its reply box, as a click on the row does; x only marks it seen.
        setBoxOpened({ column: "cursor", key: cursorRow.key });
      }
      break;
    case "b":
      // b pops a hidden inbox out by key, and tucks a key-popped one back.
      if (keyPopped) {
        setPopout("none");
      } else if (inboxHidden) {
        setPopout("key");
      }
      break;
    case "B":
      togglePin();
      break;
    case "h":
    case "ArrowLeft":
      backOneCrumb();
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
        setBoxOpened({ column: "pinned", key: cursorRow.key });
      }
      break;
    case "m":
      toggleStrips();
      break;
    case "?":
      setHelpOpen((open) => !open);
      break;
    case "Escape":
      // A key-popped inbox is tucked back, and nothing is interrupted; an open help panel closes too.
      if (keyPopped) {
        setPopout("none");
        setHelpOpen(false);
        return;
      }
      if (!helpOpen && cursorRow !== null) {
        void escapeRow({ row: cursorRow, column: "cursor", state: headState(cursorRow, cursorConversation) }, null);
      }
      setHelpOpen(false);
      return;
    default:
      return;
  }
  event.preventDefault();
}

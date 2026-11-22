import type { KeyContext } from "./keymap";

// Call is one callback or setter a key called, by its name, with its arguments.
export type Call = [name: string, ...args: unknown[]];

export interface Fake {
  context: KeyContext;
  calls: Call[];
}

// fakeContext is a context for handleKey whose callbacks and setters record their calls in order. A setter given an
// updater resolves it against helpOpen, as React does.
export function fakeContext(init: Partial<KeyContext> = {}): Fake {
  const calls: Call[] = [];
  const helpOpen = init.helpOpen ?? false;
  const record =
    (name: string) =>
    (...args: unknown[]): void => {
      calls.push([name, ...args]);
    };
  const context: KeyContext = {
    pickerOpen: false,
    openMenu: null,
    helpOpen,
    collapsed: false,
    flyoutOpen: false,
    rowCovered: false,
    projectShown: false,
    noticesOpen: false,
    index: 0,
    flat: [],
    waitKeys: [],
    cursorItem: null,
    cursorRow: null,
    cursorRowKey: "",
    cursorStep: null,
    cursorStepKey: "",
    cursorConversation: null,
    pinned: null,
    inputRef: { current: null },
    composerInputs: { current: new Map() },
    stepInputs: { current: new Map() },
    pendingG: { current: false },
    moveTo: record("moveTo"),
    waitTo: record("waitTo"),
    nextUnseen: record("nextUnseen"),
    writeSeen: (target) => {
      calls.push(["writeSeen", target]);
      return Promise.resolve();
    },
    escapeRow: (target, box) => {
      calls.push(["escapeRow", target, box]);
      return Promise.resolve();
    },
    replyBoxTarget: (box) => {
      calls.push(["replyBoxTarget", box]);
      return null;
    },
    focusCursorRow: record("focusCursorRow"),
    backOneCrumb: record("backOneCrumb"),
    openCursorPage: record("openCursorPage"),
    togglePanel: record("togglePanel"),
    answerCursor: record("answerCursor"),
    toggleOpen: record("toggleOpen"),
    closePin: record("closePin"),
    toggleInbox: record("toggleInbox"),
    expandInbox: record("expandInbox"),
    openFlyout: record("openFlyout"),
    moveFlyout: record("moveFlyout"),
    openFlyoutRow: record("openFlyoutRow"),
    closeFlyout: record("closeFlyout"),
    toggleStrips: record("toggleStrips"),
    setPickerOpen: record("setPickerOpen"),
    setMenuKey: record("setMenuKey"),
    setFilter: record("setFilter"),
    setRowOpened: record("setRowOpened"),
    setBoxOpened: record("setBoxOpened"),
    setView: record("setView"),
    setPinned: record("setPinned"),
    setPinStream: record("setPinStream"),
    closeNotices: record("closeNotices"),
    setHelpOpen: (update) => {
      calls.push(["setHelpOpen", typeof update === "function" ? update(helpOpen) : update]);
    },
    ...init,
  };
  return { context, calls };
}

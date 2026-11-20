import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { InboxRow, type RowMore } from "./inboxrow";
import type { MenuEntry } from "./menu";
import type { Row } from "./rows";
import { installTestDom, removeTestDom } from "./testdom";

// Each test's name carries the words InboxTree: the inbox row renders on its own, outside App.

const now = 1000;

// rowMore is the More of a row whose menu is shut, holding the items a row holds: Open in a new window, Mark seen when
// the row is unseen, and Archive.
function rowMore(open: boolean, unseen: boolean): RowMore {
  const items: MenuEntry[] = [
    { label: "Open in a new window", icon: "open", onSelect: () => {} },
    ...(unseen ? [{ label: "Mark seen", icon: "check", kbd: "x", onSelect: () => {} } satisfies MenuEntry] : []),
    "sep",
    { label: "Archive", icon: "archive", kbd: "A", onSelect: () => {} },
  ];
  return { open, items, toggle: () => {}, close: () => {} };
}

// A waiting pane row whose stored question asks which way, with two options.
const waitingQuestion = JSON.stringify({
  questions: [{ question: "Which way?", options: [{ label: "1 Left" }, { label: "2 Right" }] }],
});

function paneRow(init: Partial<Row>): Row {
  return {
    putOff: false,
    tool: "",
    key: "pane:box:%3",
    id: "%3",
    archiveId: "",
    kind: "pane",
    label: "build",
    project: "/work/app",
    host: "box",
    state: "waiting",
    summary: "",
    question: waitingQuestion,
    questionHash: "hash",
    changed: now - 60,
    seen: 0,
    unseen: true,
    archived: false,
    archiveKind: "none",
    ...init,
  };
}

function waitingRow(): Row {
  return paneRow({});
}

// A working pane whose tool, Bash, runs a test command, and started seventy-five seconds before now.
function workingRow(): Row {
  const tool = JSON.stringify({ id: "toolu_1", name: "Bash", input: "go test ./...", start: now - 75 });
  return paneRow({ key: "pane:box:%11", id: "%11", state: "working", question: "", questionHash: "", tool, unseen: false });
}

function sessionRow(): Row {
  return paneRow({
    key: "session:s1",
    id: "s1",
    archiveId: "s1",
    kind: "session",
    label: "notes",
    project: "",
    host: "",
    state: "done",
    summary: "wrote the guide",
    question: "",
    questionHash: "",
    archiveKind: "session",
    unseen: false,
  });
}

function terminalRow(): Row {
  return paneRow({ key: "pane:box:%23", id: "%23", state: "", label: "zsh", question: "", questionHash: "", unseen: false });
}

const idleLine = (row: Row, cursor = false, more: RowMore = rowMore(false, row.unseen)) => (
  <InboxRow row={row} now={now} cursor={cursor} more={more} onOpen={() => {}} onSeen={() => {}} />
);

async function withRow(element: JSX.Element, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("InboxTree: a waiting row leads its question with a question mark, and draws no option chips", () =>
  withRow(idleLine(waitingRow()), (container) => {
    const line = container.querySelector(".l2");
    assert.equal(line?.textContent, "Which way?", "the question is the second line");
    assert.equal(line?.classList.contains("asks"), true, "the line is marked as a question");
    assert.ok(line?.querySelector("svg.icon"), "the line leads with the question mark");
    assert.equal(container.querySelector(".opts"), null, "the row draws no option chips");
    assert.equal(container.querySelector(".opt"), null, "the row draws no option chip");
  }));

test("InboxTree: a waiting row with no question says it waits on you, with no question mark", () =>
  withRow(idleLine(paneRow({ question: "", questionHash: "" })), (container) => {
    const line = container.querySelector(".l2");
    assert.equal(line?.textContent, "waits on you");
    assert.equal(line?.classList.contains("asks"), false);
    assert.equal(line?.querySelector("svg"), null);
  }));

test("InboxTree: a working pane draws its tool, its argument and a clock from the tool's start", () =>
  withRow(idleLine(workingRow()), (container) => {
    const line = container.querySelector(".l2");
    assert.equal(line?.querySelector(".name")?.textContent, "Bash", "the tool is named");
    assert.equal(line?.textContent, "Bash go test ./... · 1:15", "the argument and the clock from seventy-five seconds before now");
  }));

test("InboxTree: a terminal pane with no state draws the idle glyph and says it has no conversation", () =>
  withRow(idleLine(terminalRow()), (container) => {
    assert.equal(container.querySelector("svg.glyph")?.getAttribute("data-state"), "idle");
    assert.equal(container.querySelector(".l2")?.textContent, "a terminal with no conversation");
  }));

test("InboxTree: a session row's title is its label alone, and its second line is its summary", () =>
  withRow(idleLine(sessionRow()), (container) => {
    assert.equal(container.querySelector(".title")?.textContent, "notes");
    assert.equal(container.querySelector(".title-dir"), null, "the row draws no directory");
    assert.equal(container.querySelector(".l2")?.textContent, "wrote the guide");
  }));

test("InboxTree: an unseen row carries data-unseen and the new dot's tip, and a seen row carries neither", async () => {
  await withRow(idleLine(waitingRow()), (container) => {
    assert.ok(container.querySelector(".b-row")?.hasAttribute("data-unseen"), "the unseen row carries data-unseen");
    assert.equal(container.querySelector(".dot")?.getAttribute("title"), "new since you looked");
  });
  await withRow(idleLine(sessionRow()), (container) => {
    assert.equal(container.querySelector(".b-row")?.hasAttribute("data-unseen"), false, "the seen row carries no data-unseen");
    assert.equal(container.querySelector(".dot")?.getAttribute("title"), null);
  });
});

test("InboxTree: the cursor's row draws b-row row cur, and a row the cursor is not on draws b-row row alone", async () => {
  await withRow(idleLine(waitingRow(), true), (container) => {
    assert.equal(container.querySelector(".b-row")?.getAttribute("class"), "b-row row cur");
    assert.equal(container.querySelector(".b-row")?.getAttribute("aria-current"), "true");
  });
  await withRow(idleLine(waitingRow(), false), (container) => {
    assert.equal(container.querySelector(".b-row")?.getAttribute("class"), "b-row row");
    assert.equal(container.querySelector(".b-row")?.hasAttribute("aria-current"), false);
  });
});

test("InboxTree: an open row carries the open class and aria-current page, and a hovered row carries the hover class", async () => {
  await withRow(
    <InboxRow row={waitingRow()} now={now} cursor={true} open={true} more={rowMore(false, true)} onOpen={() => {}} onSeen={() => {}} />,
    (container) => {
      assert.equal(container.querySelector(".b-row")?.getAttribute("class"), "b-row row cur open");
      assert.equal(container.querySelector(".b-row")?.getAttribute("aria-current"), "page");
    },
  );
  await withRow(
    <InboxRow row={waitingRow()} now={now} cursor={false} hover={true} more={rowMore(false, true)} onOpen={() => {}} onSeen={() => {}} />,
    (container) => {
      assert.equal(container.querySelector(".b-row")?.getAttribute("class"), "b-row row hover");
    },
  );
});

test("InboxTree: More's tip names what the menu holds, and the menu is drawn only while More is open", async () => {
  await withRow(idleLine(waitingRow()), (container) => {
    const more = container.querySelector<HTMLButtonElement>("button.more");
    assert.equal(more?.getAttribute("title"), "Open in a new window, Mark seen, Archive", "the tip names the items");
    assert.equal(more?.getAttribute("aria-expanded"), "false");
    assert.equal(container.querySelector('[role="menu"]'), null, "the menu is shut");
  });
  await withRow(idleLine(sessionRow(), false, rowMore(true, false)), (container) => {
    assert.equal(container.querySelector("button.more")?.getAttribute("aria-expanded"), "true");
    const items = [...container.querySelectorAll('[role="menuitem"]')].map((item) => item.querySelector("span")?.textContent);
    assert.deepEqual(items, ["Open in a new window", "Archive"], "a seen row holds no Mark seen");
  });
});

test("InboxTree: choosing a menu item calls its own onSelect and then closes the menu", async () => {
  const chosen: string[] = [];
  const closed: string[] = [];
  const more: RowMore = {
    open: true,
    items: [
      { label: "Open in a new window", icon: "open", onSelect: () => chosen.push("open") },
      { label: "Archive", icon: "archive", kbd: "A", onSelect: () => chosen.push("archive") },
    ],
    toggle: () => {},
    close: () => closed.push("close"),
  };
  await withRow(idleLine(sessionRow(), false, more), async (container) => {
    const archive = [...container.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(
      (item) => item.querySelector("span")?.textContent === "Archive",
    );
    assert.ok(archive, "the Archive item is drawn");
    await act(() => {
      archive.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
    });
    assert.deepEqual(chosen, ["archive"], "only the chosen item acts");
    assert.deepEqual(closed, ["close"], "the menu closes once it is chosen");
  });
});

test("InboxTree: a press on More does not open the row, and a click on the row opens it", async () => {
  const opened: string[] = [];
  let toggles = 0;
  const more: RowMore = { ...rowMore(false, true), toggle: () => (toggles += 1) };
  await withRow(
    <InboxRow row={waitingRow()} now={now} cursor={false} more={more} onOpen={() => opened.push("open")} onSeen={() => {}} />,
    async (container) => {
      const button = container.querySelector<HTMLElement>("button.more");
      assert.ok(button, "More is drawn");
      await act(() => {
        button.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
      });
      assert.equal(toggles, 1, "More toggles its menu");
      assert.deepEqual(opened, [], "a press on More opens nothing");
      await act(() => {
        container.querySelector<HTMLElement>(".b-row")?.dispatchEvent(new Event("click"));
      });
      assert.deepEqual(opened, ["open"], "a click on the row opens it");
    },
  );
});

test("InboxTree: a double-click calls onSeen and a click calls onOpen, each only its own", () => {
  const opened: string[] = [];
  const seen: string[] = [];
  return withRow(
    <InboxRow
      row={waitingRow()}
      now={now}
      cursor={false}
      more={rowMore(false, true)}
      onOpen={() => opened.push("open")}
      onSeen={() => seen.push("seen")}
    />,
    async (container) => {
      const row = container.querySelector<HTMLElement>(".b-row");
      assert.ok(row !== null, "the row is drawn");
      await act(() => {
        row.dispatchEvent(new Event("click"));
      });
      assert.deepEqual(opened, ["open"]);
      assert.deepEqual(seen, []);
      await act(() => {
        row.dispatchEvent(new Event("dblclick"));
      });
      assert.deepEqual(opened, ["open"]);
      assert.deepEqual(seen, ["seen"]);
    },
  );
});

test("InboxTree: without the ask, More opens the popover and no sheet", () =>
  withRow(idleLine(waitingRow(), false, rowMore(true, true)), (container) => {
    assert.ok(container.querySelector('.row-menu-list [role="menu"]'), "the popover holds the menu");
    assert.equal(container.querySelector(".sheet-scrim"), null, "no sheet is drawn");
  }));

test("InboxTree: with the ask, More opens a sheet headed by the row's title and the given line, with no key hints", () =>
  withRow(idleLine(waitingRow(), false, { ...rowMore(true, true), sheet: "tidepool · waiting on you · 4m" }), (container) => {
    const sheet = container.querySelector(".sheet-scrim > section.sheet");
    assert.equal(sheet?.getAttribute("role"), "dialog", "the sheet is a dialog in its scrim");
    assert.equal(container.querySelector(".row-menu-list"), null, "the popover is not drawn");
    const head = container.querySelector(".sheet-head");
    assert.equal(head?.querySelector("b")?.textContent, "build", "the head's title is the row's title");
    assert.ok(head?.textContent?.endsWith("tidepool · waiting on you · 4m"), "the given line follows the title");
    const items = [...container.querySelectorAll('[role="menuitem"]')].map((item) => item.querySelector("span")?.textContent);
    assert.deepEqual(items, ["Open in a new window", "Mark seen", "Archive"], "the sheet holds the row's items");
    assert.equal(container.querySelector("kbd"), null, "the items carry no key hints");
  }));

test("InboxTree: choosing an item in a sheet calls its own action once and then closes the sheet", async () => {
  const chosen: string[] = [];
  const closed: string[] = [];
  const more: RowMore = {
    open: true,
    sheet: "tidepool · waiting on you · 4m",
    items: [
      { label: "Open in a new window", icon: "open", onSelect: () => chosen.push("open") },
      { label: "Archive", icon: "archive", kbd: "A", onSelect: () => chosen.push("archive") },
    ],
    toggle: () => {},
    close: () => closed.push("close"),
  };
  await withRow(idleLine(sessionRow(), false, more), async (container) => {
    assert.ok(container.querySelector(".sheet-scrim"), "the sheet is drawn");
    const archive = [...container.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(
      (item) => item.querySelector("span")?.textContent === "Archive",
    );
    assert.ok(archive, "the Archive item is drawn");
    await act(() => {
      archive.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
    });
    assert.deepEqual(chosen, ["archive"], "only the chosen item acts");
    assert.deepEqual(closed, ["close"], "the sheet closes once it is chosen");
  });
});

test("InboxTree: a press on the sheet's scrim closes it and calls nothing, opens no row, and a press inside the dialog keeps it open", async () => {
  const chosen: string[] = [];
  const closed: string[] = [];
  const opened: string[] = [];
  const more: RowMore = {
    ...rowMore(true, true),
    sheet: "tidepool · waiting on you · 4m",
    items: [{ label: "Archive", icon: "archive", kbd: "A", onSelect: () => chosen.push("archive") }],
    close: () => closed.push("close"),
  };
  await withRow(
    <InboxRow row={waitingRow()} now={now} cursor={false} more={more} onOpen={() => opened.push("open")} onSeen={() => {}} />,
    async (container) => {
      const dialog = container.querySelector<HTMLElement>("section.sheet");
      assert.ok(dialog, "the sheet is drawn");
      await act(() => {
        dialog.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
      });
      assert.deepEqual(closed, [], "a press inside the dialog keeps the sheet open");
      const scrim = container.querySelector<HTMLElement>(".sheet-scrim");
      assert.ok(scrim, "the scrim is drawn");
      await act(() => {
        scrim.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
      });
      assert.deepEqual(closed, ["close"], "a press on the scrim closes the sheet");
      assert.deepEqual(chosen, [], "a press on the scrim calls no item");
      assert.deepEqual(opened, [], "a press on the scrim opens no row");
    },
  );
});

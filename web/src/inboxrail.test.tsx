import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { InboxRail } from "./inboxrail";
import type { FlyoutModel, FlyoutRow, RailTileModel } from "./railtiles";
import type { Row } from "./rows";
import { placeRect } from "./testlayout";
import { installTestDom, removeTestDom } from "./testdom";

// withDom gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withDom(check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.appendChild(container);
  try {
    check(container);
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
}

// draw renders the element into the container, and flushes its effects.
function draw(container: HTMLElement, element: JSX.Element): void {
  act(() => {
    render(element, container);
  });
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// labelOf names a rail child: its aria-label when it has one, else its class.
function labelOf(element: Element): string {
  return element.getAttribute("aria-label") ?? element.className;
}

// tileOf is the rail's button labelled title.
function tileOf(root: ParentNode, title: string): HTMLElement {
  return one(root, `button[aria-label="${title}"]`);
}

const now = 1_000_000;

const tiles: RailTileModel[] = [
  { key: "lighthouse", name: "lighthouse", n: 0, live: false, fresh: false, quiet: true, cur: false, title: "lighthouse" },
  { key: "tidepool", name: "tidepool", n: 3, live: true, fresh: false, quiet: false, cur: true, title: "tidepool: 3 wait on you" },
  { key: "almanac", name: "almanac", n: 0, live: false, fresh: true, quiet: false, cur: false, title: "almanac" },
];

// row is a seen, done pane row of tidepool with the key, unless init says otherwise.
function row(init: Partial<Row> & Pick<Row, "key" | "label">): Row {
  return {
    id: init.key,
    archiveId: "",
    kind: "pane",
    project: "/code/tidepool",
    host: "",
    state: "done",
    summary: "",
    question: "",
    questionHash: "",
    changed: now - 120,
    seen: 0,
    unseen: false,
    archived: false,
    putOff: false,
    archiveKind: "session",
    tool: "",
    ...init,
  };
}

const waitingRow: FlyoutRow = {
  kind: "row",
  key: "ask",
  row: row({ key: "ask", label: "Ship the tide table", state: "waiting", question: "Publish it now?", changed: now - 7200 }),
};
const epicRow: FlyoutRow = { kind: "hung", key: "epic:tide/alerts", name: "alerts", ask: "workflow node red", changed: now - 3 * 86400 };
const newRow: FlyoutRow = {
  kind: "row",
  key: "fresh",
  row: row({ key: "fresh", label: "Building the harbour feed", state: "done", unseen: true, changed: now - 60 }),
};

const tidepool: FlyoutModel = {
  key: "tidepool",
  name: "tidepool",
  path: "~/code/tidepool",
  waits: "2 wait",
  fresh: "1 new",
  rows: [epicRow, waitingRow, newRow],
  rest: "and 4 more, none waiting · 6 ended",
};

// Calls records what the rail's props were called with, in order.
type Calls = string[];

// propsOf gives the rail its props, each press recording its call into calls, with no unread notice and no flyout.
function propsOf(calls: Calls) {
  return {
    tiles,
    flyout: null,
    now,
    onMark: () => calls.push("mark"),
    onTile: (key: string) => calls.push(`tile:${key}`),
    onHover: (key: string) => calls.push(`hover:${key}`),
    onHoverOff: () => calls.push("hover-off"),
    onFlyoutRow: (item: FlyoutRow) => calls.push(`row:${item.key}`),
    onFlyoutPage: (key: string) => calls.push(`page:${key}`),
    onNew: () => calls.push("new"),
    onSearch: () => calls.push("search"),
    onNotices: () => calls.push("notices"),
    onKeys: () => calls.push("keys"),
    unread: 0,
  };
}

// pointerOn dispatches a bubbling pointer event of the type on the element.
function pointerOn(element: Element, type: string): void {
  element.dispatchEvent(new window.PointerEvent(type, { bubbles: type === "pointerover" }));
}

test("InboxRail is a nav labelled inbox, collapsed: the mark, a separator, the tiles in their own scroll area, the spring, then New, Search, Notices and Keys", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} />);
    const nav = one(container, "nav.rail.start");
    assert.equal(nav.getAttribute("aria-label"), "inbox, collapsed");
    assert.deepEqual(Array.from(nav.children).map(labelOf), [
      "Expand the inbox",
      "rail-sep",
      "rail-tiles",
      "spring",
      "New session",
      "Search",
      "Notices",
      "Keys",
    ]);
    assert.deepEqual(Array.from(one(nav, ".rail-tiles").children).map(labelOf), [
      "lighthouse",
      "tidepool: 3 wait on you",
      "almanac",
    ]);
  });
});

test("InboxRail Notices tile is bare, labelled Notices, and tips Notices with no unread notice and draws no count", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} />);
    const bell = tileOf(container, "Notices");
    assert.equal(bell.classList.contains("bare"), true);
    assert.equal(bell.getAttribute("data-tip"), "Notices");
    assert.equal(bell.getAttribute("title"), "Notices");
    assert.equal(bell.querySelector(".n"), null);
  });
});

test("InboxRail Notices tile draws its unread count in the new role, and tips Notices · N unread", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} unread={3} />);
    const bell = tileOf(container, "Notices");
    assert.equal(bell.querySelector(".n.new")?.textContent, "3");
    assert.equal(bell.getAttribute("data-tip"), "Notices · 3 unread");
    assert.equal(bell.getAttribute("title"), "Notices · 3 unread");
  });
});

test("InboxRail project tile is the project's whole name set sideways, its count above it only past zero, its dots below, cur, and its title as label", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} />);

    const quiet = tileOf(container, "lighthouse");
    assert.ok(quiet.classList.contains("vtile"), "a project tile is a vertical tile");
    assert.equal(one(quiet, ".vn").textContent, "lighthouse", "the tile names the project in full");
    assert.equal(quiet.querySelector(".n"), null, "no count for nothing waiting");
    assert.equal(quiet.querySelector(".vdots"), null, "no dots for nothing live or new");
    assert.equal(quiet.classList.contains("cur"), false);
    assert.equal(quiet.getAttribute("title"), "lighthouse");

    const waiting = tileOf(container, "tidepool: 3 wait on you");
    assert.deepEqual(
      Array.from(waiting.children).map((child) => child.className),
      ["n", "vn", "vdots"],
      "the count above the name, the dots below it",
    );
    assert.equal(one(waiting, ".n").textContent, "3");
    assert.ok(waiting.querySelector(".vdots i.live") !== null);
    assert.equal(waiting.querySelector(".vdots i.new"), null);
    assert.equal(waiting.classList.contains("cur"), true);
    assert.equal(waiting.getAttribute("title"), "tidepool: 3 wait on you");

    const fresh = tileOf(container, "almanac");
    assert.equal(fresh.querySelector(".n"), null);
    assert.equal(fresh.querySelector(".vdots i.live"), null);
    assert.ok(fresh.querySelector(".vdots i.new") !== null);
  });
});

test("InboxRail draws a quiet project's tile quiet, and a tile with something waiting or new not quiet", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} />);
    assert.equal(tileOf(container, "lighthouse").classList.contains("quiet"), true, "nothing waits and nothing is new");
    assert.equal(tileOf(container, "tidepool: 3 wait on you").classList.contains("quiet"), false, "three wait");
    assert.equal(tileOf(container, "almanac").classList.contains("quiet"), false, "one is new");
  });
});

test("InboxRail mark tile reads Expand the inbox, with its key in its tip", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} tiles={[]} />);
    const mark = tileOf(container, "Expand the inbox");
    assert.equal(mark.getAttribute("title"), "Expand the inbox  b");
    assert.equal(mark.classList.contains("brand"), true);
  });
});

test("InboxRail tiles call their props: the mark onMark, a project onTile with its key, then onNew, onSearch, onNotices and onKeys", async () => {
  await withDom((container) => {
    const calls: Calls = [];
    draw(container, <InboxRail {...propsOf(calls)} />);
    tileOf(container, "Expand the inbox").click();
    tileOf(container, "tidepool: 3 wait on you").click();
    tileOf(container, "New session").click();
    tileOf(container, "Search").click();
    tileOf(container, "Notices").click();
    tileOf(container, "Keys").click();
    assert.deepEqual(calls, ["mark", "tile:tidepool", "new", "search", "notices", "keys"]);
  });
});

test("InboxRail pointer over a project tile hovers its project, over the flyout keeps it, and over the rest of the rail or off it lets go", async () => {
  await withDom((container) => {
    const calls: Calls = [];
    draw(container, <InboxRail {...propsOf(calls)} flyout={tidepool} />);
    pointerOn(one(tileOf(container, "almanac"), ".vn"), "pointerover");
    assert.deepEqual(calls, ["hover:almanac"], "the pointer on a tile's name hovers the tile's project");
    pointerOn(one(container, ".pfly .pf-path"), "pointerover");
    assert.deepEqual(calls, ["hover:almanac"], "the pointer in the flyout keeps it");
    pointerOn(tileOf(container, "New session"), "pointerover");
    assert.deepEqual(calls, ["hover:almanac", "hover-off"], "the pointer on the rail's other tiles lets go");
    pointerOn(one(container, "nav.rail.start"), "pointerleave");
    assert.deepEqual(calls, ["hover:almanac", "hover-off", "hover-off"], "the pointer leaving the rail lets go");
  });
});

test("ProjectFlyout is drawn only when given, as a dialog named for its project, inside the rail and outside its tiles' scroll area", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} />);
    assert.equal(container.querySelector(".pfly"), null, "no flyout is drawn without one");
    draw(container, <InboxRail {...propsOf([])} flyout={tidepool} />);
    const fly = one(container, "nav.rail.start > .pfly");
    assert.equal(fly.getAttribute("role"), "dialog");
    assert.equal(fly.getAttribute("aria-label"), "tidepool");
    assert.equal(fly.closest(".rail-tiles"), null, "the flyout is not in the tiles' scroll area");
  });
});

test("ProjectFlyout heads with the project's name and counts, and its path under them", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} flyout={tidepool} />);
    const head = one(container, ".pfly .pf-head");
    assert.equal(one(head, ".pf-nm").textContent, "tidepool");
    assert.equal(one(head, ".pf-n .is-attention").textContent, "2 wait");
    assert.equal(one(head, ".pf-n .is-new").textContent, "1 new");
    assert.equal(one(head, ".pf-path").textContent, "~/code/tidepool");
  });
});

test("ProjectFlyout draws no count that is empty", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} flyout={{ ...tidepool, waits: "", fresh: "" }} />);
    assert.equal(one(container, ".pfly .pf-n").children.length, 0);
  });
});

test("ProjectFlyout lists its rows in order, the first as the cursor, each with its glyph, title and age, and what it asks in the attention role", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} flyout={tidepool} />);
    const rows = Array.from(container.querySelectorAll<HTMLElement>(".pfly .pf-row"));
    assert.deepEqual(
      rows.map((item) => item.getAttribute("data-key")),
      ["epic:tide/alerts", "ask", "fresh"],
    );
    assert.deepEqual(
      rows.map((item) => item.classList.contains("cur")),
      [true, false, false],
      "the first row is the cursor",
    );
    const [epic, ask, fresh] = rows;
    assert.equal(one(epic, ".t").textContent, "alerts");
    assert.equal(one(epic, ".age").textContent, "3d");
    assert.equal(one(epic, ".s.asks").textContent, "workflow node red");
    assert.equal(one(epic, ".glyph").getAttribute("data-state"), "waiting");
    assert.equal(one(ask, ".t").textContent, "Ship the tide table");
    assert.equal(one(ask, ".age").textContent, "2h");
    assert.equal(one(ask, ".asks").textContent, "Publish it now?", "a waiting conversation's second line is its ask");
    assert.equal(one(ask, ".glyph").getAttribute("data-state"), "waiting");
    assert.equal(fresh.hasAttribute("data-unseen"), true, "a new row is drawn new");
    assert.equal(ask.hasAttribute("data-unseen"), false);
    assert.equal(one(fresh, ".age").textContent, "1m");
  });
});

test("ProjectFlyout counts the rest on one line, and opens the project's page from its foot with its keys beside", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} flyout={tidepool} />);
    assert.equal(one(container, ".pfly .pf-more").textContent, "and 4 more, none waiting · 6 ended");
    const open = one(container, ".pfly .pf-foot button.pf-open");
    assert.equal(open.textContent, "Open tidepool’s page");
    assert.equal(open.classList.contains("cur"), false, "with rows, the page is not the cursor");
    assert.deepEqual(
      Array.from(container.querySelectorAll(".pfly .pf-keys kbd")).map((kbd) => kbd.textContent),
      ["⏎", "↑", "↓", "esc"],
    );
  });
});

test("ProjectFlyout with no rows and no rest draws neither, and its page button is the cursor", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} flyout={{ ...tidepool, rows: [], rest: "" }} />);
    assert.equal(container.querySelector(".pfly .pf-rows"), null);
    assert.equal(container.querySelector(".pfly .pf-more"), null);
    assert.equal(one(container, ".pfly button.pf-open").classList.contains("cur"), true);
  });
});

test("ProjectFlyout's rows open their rows, and its foot opens the project's page", async () => {
  await withDom((container) => {
    const calls: Calls = [];
    draw(container, <InboxRail {...propsOf(calls)} flyout={tidepool} />);
    one(container, '.pfly .pf-row[data-key="ask"]').click();
    one(container, '.pfly .pf-row[data-key="epic:tide/alerts"] .s').click();
    one(container, ".pfly button.pf-open").click();
    assert.deepEqual(calls, ["row:ask", "row:epic:tide/alerts", "page:tidepool"]);
  });
});

test("ProjectFlyout lies level with its tile's top, and is held inside the rail's height", async () => {
  await withDom((container) => {
    draw(container, <InboxRail {...propsOf([])} flyout={tidepool} />);
    const nav = one(container, "nav.rail.start");
    const tile = tileOf(container, "tidepool: 3 wait on you");
    const fly = one(container, ".pfly");
    placeRect(nav, { top: 40, bottom: 640, right: 64 });
    placeRect(fly, { top: 0, bottom: 150, right: 456 });
    placeRect(tile, { top: 240, bottom: 360, right: 52 });
    draw(container, <InboxRail {...propsOf([])} flyout={tidepool} />);
    assert.equal(fly.style.top, "200px", "the flyout's top is the tile's, from the rail's top");
    placeRect(tile, { top: 560, bottom: 630, right: 52 });
    draw(container, <InboxRail {...propsOf([])} flyout={tidepool} />);
    assert.equal(fly.style.top, "450px", "a flyout that would run past the rail's foot is held at it");
  });
});

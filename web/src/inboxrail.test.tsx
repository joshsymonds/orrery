import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { InboxRail } from "./inboxrail";
import type { RailTileModel } from "./railtiles";
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

// all returns the elements under root that match selector, in document order.
function all(root: ParentNode, selector: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(selector));
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

// tileOf is the rail's tile for a project key: its button, found by its label.
function tileOf(root: ParentNode, title: string): HTMLElement {
  return one(root, `button[aria-label="${title}"]`);
}

const tiles: RailTileModel[] = [
  { key: "lighthouse", text: "li", n: 0, live: false, fresh: false, cur: false, title: "lighthouse" },
  { key: "tidepool", text: "ti", n: 3, live: true, fresh: false, cur: true, title: "tidepool: 3 waits on you" },
  { key: "almanac", text: "al", n: 0, live: false, fresh: true, cur: false, title: "almanac" },
];

// handlersOf gives the rail its props, each recording its call into calls.
function handlersOf(calls: string[]) {
  return {
    onMark: () => calls.push("mark"),
    onTile: (key: string) => calls.push(`tile:${key}`),
    onNew: () => calls.push("new"),
    onSearch: () => calls.push("search"),
    onKeys: () => calls.push("keys"),
    onEnter: () => calls.push("enter"),
    onLeave: () => calls.push("leave"),
  };
}

test("InboxRail is a nav labelled inbox, at rest, drawing the mark, a separator, a tile per project, the spring, then New, Search and Keys", async () => {
  await withDom((container) => {
    draw(container, <InboxRail tiles={tiles} {...handlersOf([])} />);
    const nav = one(container, "nav");
    assert.equal(nav.getAttribute("aria-label"), "inbox, at rest");
    assert.deepEqual(
      Array.from(nav.children).map(labelOf),
      [
        "Open the inbox",
        "rail-sep",
        "lighthouse",
        "tidepool: 3 waits on you",
        "almanac",
        "spring",
        "New session",
        "Search",
        "Keys",
      ],
    );
  });
});

test("InboxRail draws no bell or Notices tile", async () => {
  await withDom((container) => {
    draw(container, <InboxRail tiles={tiles} {...handlersOf([])} />);
    assert.equal(container.querySelector(".bell"), null);
    assert.equal(container.querySelector('[aria-label="Notices"]'), null);
  });
});

test("InboxRail project tile shows its letters, its count only above zero, its live and new dots, cur, and its title as label", async () => {
  await withDom((container) => {
    draw(container, <InboxRail tiles={tiles} {...handlersOf([])} />);

    const quiet = tileOf(container, "lighthouse");
    assert.equal(quiet.textContent, "li");
    assert.equal(quiet.querySelector(".n"), null);
    assert.equal(quiet.querySelector(".dots"), null);
    assert.equal(quiet.classList.contains("cur"), false);
    assert.equal(quiet.getAttribute("title"), "lighthouse");

    const waiting = tileOf(container, "tidepool: 3 waits on you");
    assert.equal(waiting.querySelector(".n")?.textContent, "3");
    assert.ok(waiting.querySelector("i.live") !== null);
    assert.equal(waiting.querySelector("i.new"), null);
    assert.equal(waiting.classList.contains("cur"), true);
    assert.equal(waiting.getAttribute("title"), "tidepool: 3 waits on you");

    const fresh = tileOf(container, "almanac");
    assert.equal(fresh.querySelector(".n"), null);
    assert.equal(fresh.querySelector("i.live"), null);
    assert.ok(fresh.querySelector("i.new") !== null);
    assert.equal(fresh.classList.contains("cur"), false);
  });
});

test("InboxRail mark tile reads Open the inbox with tip Inbox  b", async () => {
  await withDom((container) => {
    draw(container, <InboxRail tiles={[]} {...handlersOf([])} />);
    const mark = tileOf(container, "Open the inbox");
    assert.equal(mark.getAttribute("data-tip"), "Inbox  b");
    assert.equal(mark.classList.contains("brand"), true);
  });
});

test("InboxRail tiles call their props: the mark onMark, a project onTile with its key, then onNew, onSearch and onKeys", async () => {
  await withDom((container) => {
    const calls: string[] = [];
    draw(container, <InboxRail tiles={tiles} {...handlersOf(calls)} />);
    tileOf(container, "Open the inbox").click();
    tileOf(container, "tidepool: 3 waits on you").click();
    tileOf(container, "New session").click();
    tileOf(container, "Search").click();
    tileOf(container, "Keys").click();
    assert.deepEqual(calls, ["mark", "tile:tidepool", "new", "search", "keys"]);
  });
});

test("InboxRail pointer enter and leave call onEnter and onLeave, leave with the related target", async () => {
  await withDom((container) => {
    const calls: string[] = [];
    let leftTo: EventTarget | null | undefined;
    draw(container, <InboxRail tiles={tiles} {...handlersOf(calls)} onLeave={(to) => { calls.push("leave"); leftTo = to; }} />);
    const nav = one(container, "nav");
    const elsewhere = document.createElement("div");
    document.body.appendChild(elsewhere);
    nav.dispatchEvent(new window.PointerEvent("pointerenter"));
    nav.dispatchEvent(new window.PointerEvent("pointerleave", { relatedTarget: elsewhere }));
    assert.deepEqual(calls, ["enter", "leave"]);
    assert.equal(leftTo, elsewhere);
    elsewhere.remove();
  });
});

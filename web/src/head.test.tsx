import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import type { AskLine } from "./asks";
import {
  ConversationContextSchema,
  ConversationStateSchema,
  HostStatusResponseSchema,
} from "./gen/orrery/v1/orrery_pb";
import type { Conversation } from "./conversation";
import { Head, type HeadMenu, type HeadProps } from "./head";
import type { Row } from "./rows";
import { installTestDom, removeTestDom } from "./testdom";

// withHead renders the element into a fresh test DOM, runs the check on its container, and removes the DOM.
async function withHead(
  element: JSX.Element,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
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

// click dispatches a click on the element and lets Preact draw what it changes.
async function click(element: Element): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true, cancelable: true }));
  });
}

const noop = () => {};

const noMark = { needs: 0, enter: noop, leave: noop, pin: noop };

function makeRow(init: Partial<Row>): Row {
  return {
    putOff: false,
    tool: "",
    key: "pane:halyard:%14",
    id: "%14",
    archiveId: "rotaplan",
    kind: "pane",
    label: "Tide table cache warm-up",
    project: "/home/owner/code/tidepool",
    host: "halyard",
    state: "working",
    summary: "",
    question: "",
    questionHash: "",
    changed: 0,
    seen: 0,
    unseen: false,
    archived: false,
    archiveKind: "none",
    ...init,
  };
}

// conversationOf is a conversation whose state holds the goal and the context given.
function conversationOf(goal: string, tokens: bigint, window: bigint): Conversation {
  const state = create(ConversationStateSchema, {
    goal,
    context: create(ConversationContextSchema, { tokens, window }),
  });
  return { transcript: "", seq: 0n, items: [], oldest: 0n, more: false, state, unfinished: [], queued: [], unsent: [] };
}

const rotaLine: AskLine = { epic: "rotaplan", slug: "rota-export", text: "asks: Bump the embedded tz database?", way: "Answer" };

const menuOf = (init: Partial<HeadMenu> = {}): HeadMenu => ({
  stripsShown: true,
  toggleStrips: noop,
  markSeen: noop,
  archive: null,
  keys: noop,
  ...init,
});

// headProps is the Head an open desktop conversation draws, with nothing asking and no strip host, unless init says so.
function headProps(init: Partial<HeadProps> = {}): HeadProps {
  return {
    row: makeRow({}),
    conversation: null,
    title: "Tide table cache warm-up",
    host: null,
    showStrip: false,
    mark: noMark,
    onBack: noop,
    note: "",
    lines: [],
    askingWords: "",
    badges: [],
    fold: "open",
    narrow: false,
    onOpenEpic: noop,
    onFind: noop,
    onHide: noop,
    onShow: noop,
    menu: menuOf(),
    ...init,
  };
}

// more is the More button of the title row.
function more(container: HTMLElement): HTMLElement {
  const button = container.querySelector<HTMLElement>(".icon.more");
  assert.notEqual(button, null, "the More button is drawn");
  return button as HTMLElement;
}

// menuItems is each item of the open More menu, as its label and the key it is listed with.
function menuItems(container: HTMLElement): Array<{ label: string; key: string | null; element: HTMLElement }> {
  return Array.from(container.querySelectorAll<HTMLElement>(".head-menu [role='menuitem']")).map((element) => ({
    label: element.firstChild?.textContent ?? "",
    key: element.querySelector("kbd")?.textContent ?? null,
    element,
  }));
}

test("HeadTitle: the place crumb is the row's directory, the page crumb is the title, and no state or blocker is drawn", async () => {
  await withHead(<Head {...headProps({ row: makeRow({ project: "/home/owner/code/tidepool" }) })} />, (container) => {
    assert.equal(container.querySelector(".crumb.place")?.textContent ?? null, "tidepool");
    assert.equal(container.querySelector(".crumb.here")?.textContent ?? null, "Tide table cache warm-up");
    assert.equal(container.querySelector(".state"), null);
    assert.equal(container.querySelector(".blocker"), null);
  });
});

test("HeadTitle: the place crumb names its host for the phone layout, and the title row holds the bare context", async () => {
  const props = headProps({
    row: makeRow({ project: "/home/owner/code/tidepool" }),
    conversation: conversationOf("", 174000n, 200000n),
    host: { host: "halyard", stats: [] },
    showStrip: true,
  });
  await withHead(<Head {...props} />, (container) => {
    const place = container.querySelector(".crumb.place");
    assert.equal(place?.getAttribute("data-host") ?? null, "halyard");
    assert.equal(place?.textContent ?? null, "tidepool", "the host is drawn by the phone layout's style, not as text");
    const bare = container.querySelector(".title-row .phone-ctx .ctx");
    assert.equal(bare?.getAttribute("data-level") ?? null, "crit");
    assert.equal(bare?.querySelector(".name"), null, "the title row's context is bare");
  });
});

test("PhoneHead: a context with no tokens draws no context holder in the title row", async () => {
  const props = headProps({ conversation: conversationOf("", 0n, 200000n), showStrip: true, narrow: true });
  await withHead(<Head {...props} />, (container) => {
    assert.equal(container.querySelector(".title-row .phone-ctx"), null, "no empty holder beside More");
  });
});

test("PhoneHead: folded, the title row keeps the bare context beside More", async () => {
  const props = headProps({ conversation: conversationOf("", 30000n, 200000n), showStrip: true, fold: "reading", narrow: true });
  await withHead(<Head {...props} />, (container) => {
    assert.notEqual(container.querySelector(".compact"), null, "the head is folded");
    assert.equal(container.querySelector(".title-row .phone-ctx .ctx")?.getAttribute("data-level") ?? null, "ok");
  });
});

test("HeadTitle: with no host the place crumb names none, and with the strip hidden the title row holds no context", async () => {
  const props = headProps({ conversation: conversationOf("", 174000n, 200000n), showStrip: false });
  await withHead(<Head {...props} />, (container) => {
    assert.equal(container.querySelector(".crumb.place")?.hasAttribute("data-host") ?? null, false);
    assert.equal(container.querySelector(".phone-ctx"), null);
  });
});

test("HeadTitle: find is titled Find  / and calls onFind", async () => {
  let finds = 0;
  await withHead(
    <Head
      {...headProps({
        onFind: () => {
          finds += 1;
        },
      })}
    />,
    async (container) => {
      const find = container.querySelector<HTMLElement>("[title='Find  /']");
      assert.equal(find?.getAttribute("aria-label") ?? null, "Find in this conversation");
      await click(find as HTMLElement);
      assert.equal(finds, 1);
    },
  );
});

test("HeadTitle: More is titled Archive, hide meters, keys, and opens a menu of its items with their keys", async () => {
  await withHead(<Head {...headProps({ menu: menuOf({ archive: noop }) })} />, async (container) => {
    assert.equal(more(container).getAttribute("title"), "Archive, hide meters, keys");
    assert.equal(more(container).getAttribute("aria-expanded"), "false");
    await click(more(container));
    assert.equal(more(container).getAttribute("aria-expanded"), "true");
    assert.deepEqual(
      menuItems(container).map((item) => [item.label, item.key]),
      [
        ["Find in this conversation", "/"],
        ["Hide the meters", "m"],
        ["Mark seen", "x"],
        ["Archive", "A"],
        ["Keys", "?"],
      ],
    );
  });
});

test("HeadTitle: each menu item runs its callback and closes the menu", async () => {
  const ran: string[] = [];
  const props = headProps({
    onFind: () => ran.push("find"),
    menu: menuOf({
      toggleStrips: () => ran.push("meters"),
      markSeen: () => ran.push("unseen"),
      archive: () => ran.push("archive"),
      keys: () => ran.push("keys"),
    }),
  });
  await withHead(<Head {...props} />, async (container) => {
    const expected = ["find", "meters", "unseen", "archive", "keys"];
    for (const [index, name] of expected.entries()) {
      await click(more(container));
      const item = menuItems(container)[index];
      assert.notEqual(item, undefined, `menu item ${index} is drawn`);
      await click(item?.element as HTMLElement);
      assert.equal(ran[index], name);
      assert.equal(container.querySelector(".head-menu"), null, "choosing an item closes the menu");
    }
  });
});

test("HeadTitle: the meters item says Show the meters while the meters are hidden", async () => {
  await withHead(<Head {...headProps({ menu: menuOf({ stripsShown: false }) })} />, async (container) => {
    await click(more(container));
    assert.equal(menuItems(container)[1]?.label, "Show the meters");
    assert.equal(menuItems(container)[1]?.key, "m");
  });
});

test("HeadTitle: Archive is listed only when archive is given", async () => {
  await withHead(<Head {...headProps({ menu: menuOf({ archive: null }) })} />, async (container) => {
    await click(more(container));
    assert.equal(
      menuItems(container).some((item) => item.label === "Archive"),
      false,
      "no archive, no Archive item",
    );
  });
  await withHead(<Head {...headProps({ menu: menuOf({ archive: noop }) })} />, async (container) => {
    await click(more(container));
    assert.equal(
      menuItems(container).some((item) => item.label === "Archive"),
      true,
      "archive given, Archive item drawn",
    );
  });
});

test("HeadTitle: Hide the header is listed only when narrow, and runs onHide", async () => {
  let hides = 0;
  const onHide = () => {
    hides += 1;
  };
  await withHead(<Head {...headProps({ narrow: false, onHide })} />, async (container) => {
    await click(more(container));
    assert.equal(
      menuItems(container).some((item) => item.label === "Hide the header"),
      false,
      "wide, no Hide the header item",
    );
  });
  await withHead(<Head {...headProps({ narrow: true, onHide })} />, async (container) => {
    await click(more(container));
    const hide = menuItems(container).find((item) => item.label === "Hide the header");
    assert.equal(hide?.key ?? null, null, "Hide the header has no key");
    await click(hide?.element as HTMLElement);
    assert.equal(hides, 1);
  });
});

test("HeadTitle: a note draws under the row as an alert", async () => {
  const note = "Could not archive this session: the server did not answer. Nothing changed.";
  await withHead(<Head {...headProps({ note })} />, (container) => {
    const alert = container.querySelector(".title-row + .page-note[role='alert']");
    assert.equal(alert?.textContent ?? null, note);
  });
});

test("Band: the open head draws the goal and an ask line, and Answer calls onOpenEpic with the epic id", async () => {
  const goal = "Every port’s tide table is cached through December 17 and the warm-up is one command.";
  const opened: string[] = [];
  await withHead(
    <Head
      {...headProps({
        conversation: conversationOf(goal, 0n, 0n),
        lines: [rotaLine],
        onOpenEpic: (epic) => opened.push(epic),
      })}
    />,
    async (container) => {
      assert.equal(container.querySelector(".subj.goal .what")?.textContent ?? null, "Goal");
      assert.equal(container.querySelector(".subj.goal .st")?.textContent ?? null, goal);
      const line = container.querySelector<HTMLElement>("[data-epic='rotaplan']");
      assert.equal(line?.querySelector(".end")?.textContent ?? null, "Answer ›");
      await click(line as HTMLElement);
      assert.deepEqual(opened, ["rotaplan"]);
    },
  );
});

test("Band: the hide control is drawn only when narrow, and calls onHide", async () => {
  const conversation = conversationOf("Ship the warm-up.", 0n, 0n);
  let hides = 0;
  const onHide = () => {
    hides += 1;
  };
  await withHead(<Head {...headProps({ conversation, narrow: false, onHide })} />, (container) => {
    assert.equal(container.querySelector(".fold-head"), null, "wide, no hide control on the goal");
  });
  await withHead(<Head {...headProps({ conversation, narrow: true, onHide })} />, async (container) => {
    const hide = container.querySelector<HTMLElement>(".subj.goal .fold-head");
    assert.equal(hide?.getAttribute("aria-label") ?? null, "Hide the header");
    await click(hide as HTMLElement);
    assert.equal(hides, 1);
  });
});

test("PhoneHead: a folded head draws the title row and CompactHead, never the strip or band", async () => {
  const conversation = conversationOf("Ship the warm-up.", 174000n, 200000n);
  const host = create(HostStatusResponseSchema, {
    host: "halyard",
    stats: [{ label: "cpu", value: "71%", percent: 71, level: "warn" }],
  });
  const props = headProps({ conversation, host, showStrip: true, lines: [rotaLine], narrow: true, askingWords: "rota-export asks" });
  await withHead(<Head {...props} fold="open" />, (container) => {
    assert.notEqual(container.querySelector(".pane-strip"), null, "open draws the strip");
    assert.notEqual(container.querySelector(".subject"), null, "open draws the band");
  });
  await withHead(<Head {...props} fold="reading" />, (container) => {
    assertFolded(container);
  });
  await withHead(<Head {...props} fold="hidden" />, (container) => {
    assertFolded(container);
  });
});

// assertFolded checks what a folded head draws: the title row and the folded line, and no strip and no band.
function assertFolded(container: HTMLElement): void {
  assert.notEqual(container.querySelector(".title-row"), null, "the title row is drawn");
  assert.notEqual(container.querySelector(".compact"), null, "the folded line is drawn");
  assert.equal(container.querySelector(".pane-strip"), null, "the strip is not drawn");
  assert.equal(container.querySelector(".subject"), null, "the band is not drawn");
}

test("PhoneHead: a tap on the folded line calls onShow", async () => {
  let shows = 0;
  let opens = 0;
  await withHead(
    <Head
      {...headProps({
        fold: "reading",
        narrow: true,
        lines: [rotaLine],
        onShow: () => {
          shows += 1;
        },
        onOpenEpic: () => {
          opens += 1;
        },
      })}
    />,
    async (container) => {
      await click(container.querySelector(".compact") as HTMLElement);
      assert.equal(shows, 1);
      assert.equal(opens, 0);
    },
  );
});

test("PhoneHead: with asking words the folded line is attention and draws them", async () => {
  await withHead(
    <Head {...headProps({ fold: "reading", narrow: true, askingWords: "rota-export asks", lines: [rotaLine] })} />,
    (container) => {
      assert.equal(container.querySelector(".compact")?.classList.contains("attention") ?? false, true);
      assert.equal(container.querySelector(".cask")?.textContent ?? null, "rota-export asks");
    },
  );
});

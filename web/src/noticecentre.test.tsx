import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { type ListedNotice, ListedNoticeSchema, type NoticeKey, NoticeKeySchema } from "./gen/orrery/v1/notice_pb";
import { NoticeCentre, type NoticeCentreProps } from "./noticecentre";
import type { NoticeHead } from "./noticewords";
import { installTestDom, removeTestDom } from "./testdom";

// now is Monday 16 November 2026 at 14:41 UTC, so Today, Yesterday and the weekday heads are fixed.
const nowIso = "2026-11-16T14:41:00Z";
const now = Date.parse(nowIso) / 1000;

function stamp(iso: string): bigint {
  return BigInt(Date.parse(iso) / 1000);
}

function sessionKey(session: string): NoticeKey {
  return create(NoticeKeySchema, { session });
}

function row(session: string, time: string, body: string, read: boolean): ListedNotice {
  return create(ListedNoticeSchema, {
    key: sessionKey(session),
    state: "done",
    title: `tidepool · session ${session}`,
    body,
    time: stamp(time),
    read,
  });
}

// head writes each row's first line the same way for every test: a glyph, a directory and the row's title.
function head(listed: ListedNotice): NoticeHead {
  return { lead: { kind: "glyph", state: listed.state }, dir: "tidepool", what: listed.title };
}

// rows are newest first: two on Today, one on Yesterday, one on Monday 9 November. The first is unread.
const rows: ListedNotice[] = [
  row("sess-a", "2026-11-16T14:31:00Z", "Which fix should I make?", false),
  row("sess-b", "2026-11-16T09:05:00Z", "Parser tests pass.", true),
  row("sess-c", "2026-11-15T18:20:00Z", "Importer refused the request.", true),
  row("sess-d", "2026-11-09T08:47:00Z", "Held by the weekly limit.", true),
];

// calls records what the centre's handlers were called with.
interface Calls {
  opened: ListedNotice[];
  readAll: number;
  closed: number;
}

// recorder is a fresh Calls and the handlers that fill it.
function recorder(): { calls: Calls; handlers: Pick<NoticeCentreProps, "onOpen" | "onReadAll" | "onClose"> } {
  const calls: Calls = { opened: [], readAll: 0, closed: 0 };
  return {
    calls,
    handlers: {
      onOpen: (listed) => calls.opened.push(listed),
      onReadAll: () => {
        calls.readAll++;
      },
      onClose: () => {
        calls.closed++;
      },
    },
  };
}

// withCentre draws the centre with props into a fresh DOM, runs check with the container, and removes the DOM.
async function withCentre(
  props: Partial<NoticeCentreProps>,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(
        <NoticeCentre
          rows={rows}
          failed={false}
          head={head}
          now={now}
          timeZone="UTC"
          narrow={false}
          onOpen={() => undefined}
          onReadAll={() => undefined}
          onClose={() => undefined}
          {...props}
        />,
        container,
      );
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

// texts reads the text of every element matching selector, in document order.
function texts(container: HTMLElement, selector: string): string[] {
  return [...container.querySelectorAll(selector)].map((element) => element.textContent ?? "");
}

// rowButtons are the rows the centre draws: each is one flat button.
function rowButtons(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>("button.notice.flat")];
}

// buttonNamed finds the button whose text is label, or fails.
function buttonNamed(container: HTMLElement, label: string): HTMLElement {
  const found = [...container.querySelectorAll<HTMLElement>("button")].find((button) => button.textContent === label);
  assert.ok(found !== undefined, `no button reads ${label}`);
  return found;
}

function click(element: HTMLElement): void {
  element.dispatchEvent(new Event("click", { bubbles: true }));
}

test("NoticeCentre: the rows fall under day heads Today, Yesterday and the weekday and date, newest first", async () => {
  await withCentre({}, (container) => {
    assert.deepEqual(texts(container, ".day"), ["Today", "Yesterday", "Monday 9 November"]);
    assert.deepEqual(
      rowButtons(container).map((button) => button.querySelector(".b")?.textContent),
      rows.map((listed) => listed.body),
      "the rows are not in the order Notices.List gave them, under their days",
    );
  });
});

test("NoticeCentre: a row draws its clock in place of its age, and holds no button but itself", async () => {
  await withCentre({}, (container) => {
    const [first] = rowButtons(container);
    assert.equal(first.querySelector(".when")?.textContent, "14:31");
    assert.equal(first.querySelectorAll("button").length, 0, "a row holds a button of its own");
    assert.equal(first.querySelector(".acts"), null, "a row holds controls");
  });
});

test("NoticeCentre: an unread row is marked unread, and a read row is not", async () => {
  await withCentre({}, (container) => {
    const buttons = rowButtons(container);
    assert.ok(buttons[0].classList.contains("unread"), "the unread row is not marked");
    assert.ok(!buttons[1].classList.contains("unread"), "the read row is marked");
  });
});

test("NoticeCentre: a click on a row calls onOpen with that row", async () => {
  const { calls, handlers } = recorder();
  await withCentre(handlers, (container) => {
    click(rowButtons(container)[1]);
    assert.equal(calls.opened.length, 1, "the click did not open exactly one row");
    assert.equal(calls.opened[0], rows[1], "the click opened a different row");
  });
});

test("NoticeCentre: Mark all read is shown while a row is unread, and calls onReadAll", async () => {
  const { calls, handlers } = recorder();
  await withCentre(handlers, (container) => {
    click(buttonNamed(container, "Mark all read"));
    assert.equal(calls.readAll, 1, "Mark all read did not call onReadAll once");
  });
});

test("NoticeCentre: Mark all read is not shown when every row is read", async () => {
  const allRead = rows.map((listed) => create(ListedNoticeSchema, { ...listed, read: true }));
  await withCentre({ rows: allRead }, (container) => {
    const labels = texts(container, "button");
    assert.ok(!labels.includes("Mark all read"), "Mark all read is shown with no unread row");
  });
});

test("NoticeCentre: Close calls onClose", async () => {
  const { calls, handlers } = recorder();
  await withCentre(handlers, (container) => {
    click(container.querySelector<HTMLElement>("button[aria-label='Close']") as HTMLElement);
    assert.equal(calls.closed, 1, "Close did not call onClose once");
  });
});

test("NoticeCentre: with no rows it says what collects there", async () => {
  await withCentre({ rows: [] }, (container) => {
    assert.deepEqual(texts(container, ".blank span"), [
      "No notices.",
      "Questions, endings and failures from sessions and epics you are not looking at collect here.",
    ]);
    assert.equal(container.querySelector(".day"), null, "an empty centre draws a day head");
  });
});

test("NoticeCentre: a failed read says so above the rows", async () => {
  await withCentre({ failed: true }, (container) => {
    const [first] = [...container.querySelectorAll(".side-note, .day")];
    assert.equal(first?.textContent, "The notices could not be read.", "the failed line is not above the first day");
  });
});

test("NoticeCentre: wide it is an overlay dialog, and narrow it is a Notices page with Back that calls onClose", async () => {
  const { calls, handlers } = recorder();
  await withCentre(handlers, (container) => {
    const dialog = container.querySelector("section.ncentre[role='dialog']");
    assert.ok(dialog !== null, "wide, the centre is not an overlay dialog");
    assert.equal(dialog.querySelector("h2")?.textContent, "Notices");
  });
  await withCentre({ ...handlers, narrow: true }, (container) => {
    assert.equal(container.querySelector("[role='dialog']"), null, "narrow, the centre is still a dialog");
    assert.equal(container.querySelector(".crumb.here")?.textContent, "Notices");
    click(container.querySelector<HTMLElement>("button.back") as HTMLElement);
    assert.equal(calls.closed, 1, "Back did not call onClose");
  });
});

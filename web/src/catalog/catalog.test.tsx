import assert from "node:assert/strict";
import { test } from "node:test";

import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { InboxRow } from "../inboxrow.stories";
import { mockTimers } from "../testclock";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "../testdom";
import { turn, until } from "../testwait";
import { CatalogIndex, catalogStories, StoryView } from "./catalog";
import { storyModules } from "./registry.gen";
import { storyName } from "./story";

// kebabName is a story's name: component/state, each part in kebab case.
const kebabName = /^[a-z0-9]+(-[a-z0-9]+)*\/[a-z0-9]+(-[a-z0-9]+)*$/;

// registeredNames is every story name the registry holds, read from its modules without catalogStories.
function registeredNames(): string[] {
  return Object.values(storyModules).flatMap((exports) =>
    Object.entries(exports).flatMap(([exportName, stories]) =>
      Object.keys(stories as Record<string, unknown>).map((state) => storyName(exportName, state)),
    ),
  );
}

// mounted renders element into a DOM that is not in the document, which runs no iframe's page, runs check on it, and
// removes the DOM when check returns or throws.
async function mounted(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(element, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("Catalog: the index lists every registered story as component/state in kebab case, including app/attention, page/list-home and each inbox-row state", async () => {
  const entries = catalogStories(storyModules);
  const registered = registeredNames().sort();
  await mounted(<CatalogIndex entries={entries} hash="" />, (container) => {
    const listed = Array.from(container.querySelectorAll("a[data-story]"), (link) => link.getAttribute("data-story") ?? "");
    assert.deepEqual(listed, registered, "the index does not list every registered story");
    assert.ok(listed.every((name) => kebabName.test(name)), "a listed name is not component/state in kebab case");
    assert.ok(listed.includes("app/attention"), "app/attention is not listed");
    assert.ok(listed.includes("page/list-home"), "page/list-home is not listed");
    for (const state of Object.keys(InboxRow)) {
      assert.ok(listed.includes(storyName("InboxRow", state)), `inbox-row/${state} is not listed`);
    }
  });
});

test("Catalog: catalogStories throws on a state key that is not kebab case, and on a name two stories share", () => {
  assert.throws(
    () => catalogStories({ "widget.stories.tsx": { Widget: { BadState: () => null } } }),
    /widget\.stories\.tsx export Widget has the state "BadState", which is not kebab case/,
  );
  assert.throws(
    () =>
      catalogStories({
        "a.stories.tsx": { InboxRow: { waiting: () => null } },
        "b.stories.tsx": { InboxRow: { waiting: () => null } },
      }),
    /b\.stories\.tsx export InboxRow shares the name inbox-row\/waiting with a\.stories\.tsx export InboxRow/,
  );
});

test("Catalog: catalogStories throws on an export that is not a record of functions", () => {
  assert.throws(() => catalogStories({ "c.stories.tsx": { Thing: () => null } }), /c\.stories\.tsx export Thing is not a record of functions/);
  assert.throws(() => catalogStories({ "c.stories.tsx": { Thing: { idle: 3 } } }), /c\.stories\.tsx export Thing is not a record of functions/);
});

test("Catalog: each frame of the story the hash names is an iframe at 1600, 1000 and 412 wide, whose src is that story's own address", async () => {
  const name = "inbox-row/waiting-question";
  await mounted(<CatalogIndex entries={catalogStories(storyModules)} hash={`#${name}`} />, (container) => {
    const frames = Array.from(container.querySelectorAll("iframe"));
    assert.deepEqual(frames.map((frame) => frame.getAttribute("width")), ["1600", "1000", "412"]);
    assert.deepEqual(frames.map((frame) => frame.getAttribute("data-width")), ["1600", "1000", "412"]);
    assert.deepEqual(frames.map((frame) => frame.getAttribute("src")), [`?story=${name}`, `?story=${name}`, `?story=${name}`]);
  });
});

test("Catalog: StoryView draws one story alone in #story, and a name the catalog does not hold draws data-story-failure naming it", async () => {
  const entries = catalogStories(storyModules);
  await mounted(<StoryView entries={entries} name="inbox-row/waiting-question" />, (container) => {
    assert.equal(container.querySelectorAll("#story .b-row").length, 1, "the story does not draw one line");
    assertNone(container.querySelector("a[data-story]"), "the index is drawn beside the story");
  });
  await mounted(<StoryView entries={entries} name="nosuch/none" />, (container) => {
    const failure = container.querySelector("[data-story-failure]");
    assert.ok(failure !== null, "no failure element is drawn");
    assert.ok((failure.textContent ?? "").includes("nosuch/none"), "the failure does not name the story");
  });
});

test("Catalog: page stories read the fixtures' instant, liveEpoch, not the wall clock", async (t) => {
  mockTimers(t, ["Date", "setTimeout"]);
  // The wall clock is days after the fixtures' instant, so a page that read Date.now would age its lines by days.
  t.mock.timers.setTime(Date.parse("2026-11-20T09:00:00Z"));
  const window = installTestDom();
  window.innerWidth = 1280;
  const root = document.createElement("div");
  document.body.append(root);
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, root);
    });
    root.remove();
    await removeTestDom(window);
  });
  try {
    await act(() => {
      render(<StoryView entries={catalogStories(storyModules)} name="page/list-home" />, root);
    });
    // Pane %12 changed at 14:37 and the fixtures' instant is 14:41, so its line reads 4m at that instant alone.
    const age = () => root.querySelector('.b-row[data-key="pane:halyard:%12"] .age')?.textContent ?? "";
    await until(t.signal, () => age() !== "", "the page's line for pane %12 with its age", { step: () => act(turn) });
    assert.equal(age(), "4m", "the age is not read at the fixtures' instant");
  } finally {
    await teardown();
  }
});

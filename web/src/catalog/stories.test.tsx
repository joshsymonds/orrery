import assert from "node:assert/strict";
import { readdirSync } from "node:fs";
import { sep } from "node:path";
import { type TestContext, test } from "node:test";
import { fileURLToPath } from "node:url";

import { type VNode, render } from "preact";
import { act } from "preact/test-utils";

import { createFixtureTransport } from "../fixtures/transport";
import * as main from "../main";
import type { Address } from "../router";
import { mockTimers } from "../testclock";
import { installTestDom, removeTestDom, teardownOnce } from "../testdom";
import { turn, until } from "../testwait";
import { componentFiles, componentModules } from "./components.gen";
import { catalogStories } from "./catalog";
import { missingStories, staleRenames, storyExemptions, storyRenames } from "./coverage";
import { PageStory } from "./pagestory";
import { storyModules } from "./registry.gen";

// wideWidth is the window width the stories draw at, as withStory in fixtures/transport.test.ts sets it.
const wideWidth = 1280;

// settleTurns is how many turns in a row the root's innerHTML must hold still before a story counts as settled.
const settleTurns = 5;

const attention: Address = { order: "attention", entry: null };

// storyDrew reports whether a story has drawn into root. The root must hold an element, and when PageStory's wrapper is
// drawn, the wrapper must hold an element of its own that is not a failure. An empty wrapper, which PageStory draws when
// App draws nothing, has not drawn.
function storyDrew(root: HTMLElement): boolean {
  if (root.querySelector("*") === null) {
    return false;
  }
  const wrapper = root.querySelector("[data-page-story]");
  return wrapper === null || wrapper.querySelector("*:not([data-story-failure])") !== null;
}

// storyProblem names what keeps the story drawn into root from working, or returns null when it draws and reports
// nothing. errors holds the arguments of each console.error call. It reports a root that holds no element, a failure
// element drawn, a page wrapper that holds nothing but its failures, and any console.error call.
function storyProblem(root: HTMLElement, errors: readonly (readonly unknown[])[]): string | null {
  if (root.querySelector("*") === null) {
    return "the story drew no element";
  }
  const failure = root.querySelector("[data-story-failure]");
  if (failure !== null) {
    return `the story drew a failure: ${failure.textContent ?? ""}`;
  }
  if (!storyDrew(root)) {
    return "the page's wrapper holds no element of its own, so App drew nothing";
  }
  if (errors.length > 0) {
    return `console.error was called: ${errors.map((args) => args.map(String).join(" ")).join("; ")}`;
  }
  return null;
}

// settled returns a check that holds once storyDrew holds on root and root's innerHTML has been unchanged for settleTurns
// checks in a row. until calls the check once per turn, so each call counts one turn.
function settled(root: HTMLElement): () => boolean {
  let last: string | null = null;
  let still = 0;
  return () => {
    const html = root.innerHTML;
    still = html === last ? still + 1 : 0;
    last = html;
    return storyDrew(root) && still >= settleTurns;
  };
}

// mountStory mounts story into a fresh DOM at the wide width, with console.error recorded, waits until the story has drawn
// and settled, then runs check with the root and the recorded calls. The story is unmounted and its DOM removed when check
// returns or throws, or as soon as the test's signal aborts. The test must call mockTimers first.
async function mountStory(
  t: TestContext,
  story: () => VNode,
  check: (root: HTMLElement, errors: unknown[][]) => void,
): Promise<void> {
  const errors: unknown[][] = [];
  t.mock.method(console, "error", (...args: unknown[]) => {
    errors.push(args);
  });
  const window = installTestDom();
  window.innerWidth = wideWidth;
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
      render(story(), root);
    });
    await until(t.signal, settled(root), "the story's drawing", { step: () => act(turn) });
    check(root, errors);
  } finally {
    await teardown();
  }
}

test("StoryCoverage: every component export of the registry's component files has a story", () => {
  assert.deepEqual(missingStories(componentModules, storyModules), []);
});

test("StoryCoverage: the component files componentModules does not import are exactly the storyExemptions, each with a reason", () => {
  const skipped = componentFiles.filter((file) => !Object.hasOwn(componentModules, file)).sort();
  assert.deepEqual(skipped, Object.keys(storyExemptions).sort());
  for (const [file, reason] of Object.entries(storyExemptions)) {
    assert.notEqual(reason.trim(), "", `${file} has no reason`);
  }
});

test("StoryCoverage: componentFiles lists exactly the .tsx files on disk under web/src that are not tests, not stories and not under catalog/", () => {
  // This file is bundled to web/build/catalog, so web/src is two directories up.
  const srcDir = fileURLToPath(new URL("../../src/", import.meta.url));
  const onDisk = readdirSync(srcDir, { encoding: "utf8", recursive: true })
    .map((entry) => entry.replaceAll(sep, "/"))
    .filter(
      (file) =>
        file.endsWith(".tsx") &&
        !file.endsWith(".test.tsx") &&
        !file.endsWith(".stories.tsx") &&
        !file.startsWith("catalog/"),
    )
    .sort();
  assert.deepEqual(onDisk, [...componentFiles].sort());
  assert.ok(onDisk.includes("app.tsx"), "app.tsx is not on disk under web/src, so the directory is wrong");
  assert.ok(onDisk.includes("main.tsx"), "main.tsx is not on disk under web/src, so the directory is wrong");
});

test("StoryCoverage: each storyExemptions file exports no component", () => {
  const exemptModules = { "main.tsx": main };
  assert.deepEqual(Object.keys(exemptModules).sort(), Object.keys(storyExemptions).sort());
  assert.deepEqual(
    missingStories(exemptModules, {}),
    [],
    "with no stories module, missingStories reports every component export, so an empty list shows each exempt file exports none",
  );
  assert.deepEqual(missingStories({ "main.tsx": { ...main, Widget: () => null } }, {}), [
    "main.tsx export Widget has no story in main.stories.tsx",
  ]);
});

test("StoryCoverage: missingStories reports a component export with no story, when its stories module is missing and when that module lacks it", () => {
  const widget = () => null;
  const line = "widget.tsx export Widget has no story in widget.stories.tsx";
  assert.deepEqual(missingStories({ "widget.tsx": { Widget: widget } }, {}), [line], "the stories module is missing");
  assert.deepEqual(
    missingStories({ "widget.tsx": { Widget: widget } }, { "widget.stories.tsx": { Gadget: {} } }),
    [line],
    "the stories module lacks Widget",
  );
  assert.deepEqual(
    missingStories({ "widget.tsx": { Widget: widget } }, { "widget.stories.tsx": { Widget: {} } }),
    [],
    "the stories module holds Widget",
  );
});

test("StoryCoverage: missingStories reports neither a lowercase function nor a number export", () => {
  assert.deepEqual(missingStories({ "widget.tsx": { helper: () => null, LIMIT: 184 } }, {}), []);
});

test("StoryCoverage: a component export whose story export is renamed through the table is covered by that story export, not by its own name", () => {
  const widget = () => null;
  const renames = { "widget.tsx": { Widget: "Gadget" } };
  assert.deepEqual(
    missingStories({ "widget.tsx": { Widget: widget } }, { "widget.stories.tsx": { Gadget: {} } }, renames),
    [],
    "the stories module holds the renamed Gadget",
  );
  assert.deepEqual(
    missingStories({ "widget.tsx": { Widget: widget } }, { "widget.stories.tsx": { Widget: {} } }, renames),
    ["widget.tsx export Widget has no story in widget.stories.tsx"],
    "the stories module holds Widget but not the renamed Gadget",
  );
});

test("StoryCoverage: an entry whose component file or component export does not exist is reported, one line each", () => {
  const widget = () => null;
  assert.deepEqual(
    staleRenames({ "gone.tsx": { Gone: "Dock" } }, { "widget.tsx": { Widget: widget } }, {}),
    ["gone.tsx export Gone renames to Dock: gone.tsx is not a component file"],
    "the component file does not exist",
  );
  assert.deepEqual(
    staleRenames({ "widget.tsx": { Gadget: "Dock" } }, { "widget.tsx": { Widget: widget } }, { "widget.stories.tsx": { Dock: {} } }),
    ["widget.tsx export Gadget renames to Dock: widget.tsx exports no component Gadget"],
    "the component export does not exist",
  );
});

test("StoryCoverage: an entry whose story export does not exist is reported, when its stories module is missing and when that module lacks it", () => {
  const widget = () => null;
  const line = "widget.tsx export Widget renames to Dock: widget.stories.tsx exports no Dock";
  assert.deepEqual(staleRenames({ "widget.tsx": { Widget: "Dock" } }, { "widget.tsx": { Widget: widget } }, {}), [line], "the stories module is missing");
  assert.deepEqual(
    staleRenames({ "widget.tsx": { Widget: "Dock" } }, { "widget.tsx": { Widget: widget } }, { "widget.stories.tsx": { Gadget: {} } }),
    [line],
    "the stories module lacks Dock",
  );
});

test("StoryCoverage: the real table reports no stale entry", () => {
  assert.deepEqual(staleRenames(storyRenames, componentModules, storyModules), []);
});

test("StoryRenders: storyProblem reports a page wrapper that holds no element but its failures", async (t) => {
  mockTimers(t, ["setTimeout"]);
  const window = installTestDom();
  const root = document.createElement("div");
  try {
    // What PageStory draws when App draws nothing: its empty wrapper, and with a refused call, the failure in it.
    root.innerHTML = '<div data-page-story=""></div>';
    assert.equal(storyDrew(root), false, "an empty wrapper counts as drawn");
    assert.notEqual(storyProblem(root, []), null, "an empty wrapper is not reported");
    root.innerHTML = '<div data-page-story=""><p data-story-failure="">not served by the fixture: orrery.v1.Picker/Ask</p></div>';
    assert.equal(storyDrew(root), false, "a wrapper holding only a failure counts as drawn");
    assert.notEqual(storyProblem(root, []), null, "a wrapper holding only a failure is not reported");
  } finally {
    await removeTestDom(window);
  }
});

test("StoryRenders: a page story whose fixture is called for a procedure it does not serve is reported as failed, naming it", (t) => {
  mockTimers(t, ["setTimeout"]);
  const fixture = createFixtureTransport();
  const story = () => (
    <PageStory
      address={attention}
      fixture={fixture}
      after={async () => {
        // The fixture refuses the call and records it. The refusal is what the story is checked for, so the rejection is
        // caught here and the failure element draws it.
        await fixture.clients.picker.ask({}).catch(() => undefined);
      }}
    />
  );
  return mountStory(t, story, (root, errors) => {
    const problem = storyProblem(root, errors);
    assert.ok(problem?.includes("orrery.v1.Picker/Ask"), `the problem does not name Picker.Ask: ${problem}`);
  });
});

for (const { name, story } of catalogStories(storyModules)) {
  test(`StoryRenders: ${name}`, (t) => {
    mockTimers(t, ["setTimeout"]);
    return mountStory(t, story, (root, errors) => {
      assert.equal(storyProblem(root, errors), null, `${name} draws a problem`);
    });
  });
}

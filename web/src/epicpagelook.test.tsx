import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { type EpicCommands, EpicPage } from "./epicpage";
import type { LoadArtifact } from "./evidence";
import { type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { Icon, type IconName } from "./glyphs";
import { installTestDom, removeTestDom } from "./testdom";

// now is the clock the page measures its times against, in unix seconds.
const now = 10_000;

// liveEpic is a live epic with no outcome, named demo-epic. It is a record epic.
const liveEpic = { id: "e1", slug: "demo-epic", repository: "demo-repo", outcome: "", live: true };

// orrerydEpic is a live orreryd epic in accepted: its outcome is its phase. Each fixture names the phase it changes.
const orrerydEpic = { ...liveEpic, source: "orreryd", outcome: "accepted" };

// repoPath is the repository path the ended epic names, which its Report reads.
const repoPath = "/work/demo-repo";

// contract is an orreryd epic's contract, accepted at zero so its words carry no time.
const contract = { title: "Show the accepted contract", care: "high", accepted: 0n };

// build is a live build: task t1 is running and names R1, and task t2 is planned and names R2.
const build = {
  epic: liveEpic,
  buildTasks: [
    { id: "t1", title: "Login", status: "dispatched", requirements: ["R1"] },
    { id: "t2", title: "Logout", status: "", requirements: ["R2"] },
  ],
  requirements: [
    { id: "R1", text: "A user signs in" },
    { id: "R2", text: "A user signs out" },
  ],
};

// detail makes the epic's response from its fields.
function detail(init: MessageInitShape<typeof EpicsGetResponseSchema>): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

// planning is an accepted orreryd epic with its summary and contract: its Contract door, and its Requirements.
const planning = detail({ ...build, buildTasks: [], epic: orrerydEpic, summary: "Ship the demo", contract });

// reviewing is a reviewing orreryd epic with one reviewer and one finding, the Review section's rows.
const reviewing = detail({
  ...build,
  epic: { ...orrerydEpic, outcome: "reviewing" },
  summary: "Ship the demo",
  contract,
  reviews: [{ position: 1n, reviewer: "security", reason: "The token is checked", verdict: "holds" }],
  ledger: [
    {
      id: "security-1",
      position: 1n,
      reviewer: "security",
      claim: "Token not checked",
      location: "auth.go:12",
      result: "",
    },
  ],
});

// releasing is an orreryd epic releasing: its step 1 passed and its step 2 is running.
const releasing = detail({
  ...build,
  epic: { ...orrerydEpic, outcome: "releasing" },
  summary: "Ship the demo",
  contract,
  releaseSteps: [
    { position: 1n, step: "Tag the release", target: "origin", undo: "git tag -d", state: "passed" },
    { position: 2n, step: "Push the branch", target: "origin", undo: "none", state: "running" },
  ],
});

// released is an orreryd epic that released, with its Report drawn.
const released = detail({
  ...build,
  epic: { ...orrerydEpic, outcome: "released", live: false, path: repoPath },
  report: { outcome: "released", shipped: ["R1"], notShipped: [], why: "" },
});

// commands are the page's commands, none of which a check here presses.
const commands: EpicCommands = {
  pause: () => {},
  resume: () => {},
  stop: () => {},
  retry: () => {},
  answer: async () => "",
  release: async () => ({ kind: "landed" }),
  close: async () => ({ kind: "landed" }),
  reread: async () => true,
};

// noArtifact reads no artifact: no check here opens a log.
const noArtifact: LoadArtifact = async () => {
  throw new Error("no artifact is read in this test");
};

// withPage renders the epic page for the detail into a fresh test DOM, runs the check on its container, and removes the
// DOM however the check ends.
async function withPage(epic: EpicsGetResponse, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    await act(() => {
      render(
        <EpicPage
          detail={epic}
          history={[]}
          now={now}
          loadArtifact={noArtifact}
          commands={commands}
          numberKeys={false}
          commandError=""
          onOpenContract={() => {}}
          onOpenSession={() => {}}
          onBack={() => {}}
        />,
        container,
      );
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

// iconPath is the drawing Icon makes for the name, so a check compares an icon by what it draws.
function iconPath(name: IconName): string | null {
  const host = document.createElement("div");
  render(<Icon name={name} />, host);
  const path = host.querySelector("path")?.getAttribute("d") ?? null;
  render(null, host);
  return path;
}

// assertIcon checks that the element is an icon, and that it draws the named icon.
function assertIcon(element: Element | undefined, name: IconName): void {
  assert.ok(element !== undefined && element.classList.contains("icon"), `the ${name} icon is drawn`);
  assert.equal(element.querySelector("path")?.getAttribute("d"), iconPath(name), `the ${name} icon draws its drawing`);
}

// assertHead checks a section's head: its icon first, in the head's hue, then its name, then its count when it draws one.
function assertHead(section: Element | null, icon: IconName, hue: string, title: string, count: string | null): void {
  assert.ok(section !== null, `the ${title} section is drawn`);
  const head = section.firstElementChild;
  assert.ok(head !== null && head.classList.contains("sec"), `the ${title} section opens with its head`);
  assert.equal(head.getAttribute("data-hue"), hue, `the ${title} head takes its hue`);
  const parts = [...head.children];
  assert.deepEqual(
    parts.map((part) => part.getAttribute("class")),
    count === null ? ["icon", "name"] : ["icon", "name", "n"],
    `the ${title} head draws its icon, its name and its count in that order`,
  );
  assertIcon(parts[0], icon);
  assert.equal(parts[1]?.textContent, title);
  if (count !== null) {
    assert.equal(parts[2]?.textContent, count);
  }
}

// assertDoor checks a door: its icon, then its label, then its text, then the chevron.
function assertDoor(door: Element | null, icon: IconName, label: string, text: string): void {
  assert.ok(door !== null, `the ${label} door is drawn`);
  const parts = [...door.children];
  assert.equal(parts.length, 4, `the ${label} door draws four parts`);
  assertIcon(parts[0], icon);
  assert.equal(parts[1]?.getAttribute("class"), "door-label");
  assert.equal(parts[1]?.textContent, label);
  assert.equal(parts[2]?.textContent, text);
  assertIcon(parts[3], "chevronRight");
}

const css = readFileSync(new URL("../src/style.css", import.meta.url), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

// declaredValue is the value the last rule naming the selector sets for the property, or undefined when none does.
function declaredValue(selector: string, property: string): string | undefined {
  let value: string | undefined;
  for (const rule of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selectors = rule[1].split(",").map((part) => part.trim().replace(/\s+/g, " "));
    if (!selectors.includes(selector)) {
      continue;
    }
    for (const declaration of rule[2].split(";")) {
      const colon = declaration.indexOf(":");
      if (colon > 0 && declaration.slice(0, colon).trim() === property) {
        value = declaration.slice(colon + 1).trim();
      }
    }
  }
  return value;
}

test("EpicPageLook: the Now head draws the clock icon in the live hue, then its name, and no count", () =>
  withPage(detail(build), (container) => {
    assertHead(container.querySelector("section.now"), "clock", "live", "Now", null);
  }));

test("EpicPageLook: the Tasks head draws the pools icon in series 4, then its name and its done count", () =>
  withPage(detail(build), (container) => {
    assertHead(container.querySelector("section.tasks"), "pools", "4", "Tasks", "0 of 2 done");
  }));

test("EpicPageLook: the Requirements head draws the check icon in the ok role, then its name and its proven count", () =>
  withPage(planning, (container) => {
    assertHead(container.querySelector("section.requirements"), "check", "ok", "Requirements", "0 of 2 proven");
  }));

test("EpicPageLook: the Review head draws the search icon in series 2, then its name and its count words", () =>
  withPage(reviewing, (container) => {
    assertHead(container.querySelector("section.review"), "search", "2", "Review", "1 reviewer · 1 finding");
  }));

test("EpicPageLook: the Release head draws the open icon in the engine role, then its name and its step count", () =>
  withPage(releasing, (container) => {
    assertHead(container.querySelector("section.release"), "open", "engine", "Release", "step 2 of 2");
  }));

test("EpicPageLook: the Report head draws the doc icon in series 5, then its name, and no count", () =>
  withPage(released, (container) => {
    assertHead(container.querySelector("section.report"), "doc", "5", "Report", null);
  }));

test("EpicPageLook: the Contract door draws the doc icon, its label, its text and the chevron", () =>
  withPage(planning, (container) => {
    assertDoor(
      container.querySelector(".summary .door.contract"),
      "doc",
      "Contract",
      "accepted · high care · 2 requirements",
    );
  }));

test("EpicPageLook: the Steered from door draws the chat icon, its label, its title and the chevron", () =>
  withPage(detail({ ...build, epic: { ...orrerydEpic, steering: "s9" }, summary: "Ship the demo" }), (container) => {
    assertDoor(container.querySelector(".summary .door.steered"), "chat", "Steered from", "session");
  }));

test("EpicPageLook: a head's icon is drawn in the head's hue, and its name in the label face", () => {
  assert.equal(declaredValue(".sec > .icon", "color"), "var(--hue)");
  assert.equal(declaredValue(".sec .name", "font"), "var(--o-type-label)");
});

test("EpicPageLook: each hue a head takes is its role or series colour in style.css", () => {
  const hues: [string, string][] = [
    ['.sec[data-hue="live"]', "var(--o-role-live)"],
    ['.sec[data-hue="4"]', "var(--o-series-4)"],
    ['.sec[data-hue="ok"]', "var(--o-role-ok)"],
    ['.sec[data-hue="2"]', "var(--o-series-2)"],
    ['.sec[data-hue="engine"]', "var(--o-role-engine)"],
    ['.sec[data-hue="5"]', "var(--o-series-5)"],
  ];
  for (const [selector, token] of hues) {
    assert.equal(declaredValue(selector, "--hue"), token, selector);
  }
});

test("EpicPageLook: a door's label takes the reference role, and its icon and chevron the ink.4 tone", () => {
  assert.equal(declaredValue(".epic-page .door > .door-label", "color"), "var(--o-role-ref)");
  assert.equal(declaredValue(".epic-page .door > .icon", "color"), "var(--o-ink-4)");
});

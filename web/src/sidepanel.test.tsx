import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";

import { at, liveEpoch } from "./fixtures/clock";
import { type BackgroundTask, BackgroundTaskSchema } from "./gen/orrery/v1/orrery_pb";
import { ListPanel, SidePanel } from "./sidepanel";
import { installTestDom, removeTestDom } from "./testdom";

// Each test's name carries the token SidePanel or WorkLists: SidePanel is the frame a thing opens in, and WorkLists is
// the agents and running lists drawn inside it. now is the fixtures' instant, in unix seconds, so no test reads the
// wall clock.

const now = Date.parse(liveEpoch) / 1000;
const href = "#/attention";

async function withDom(element: JSX.Element, check: (container: HTMLElement) => void): Promise<void> {
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

function task(init: { description: string; subagentType?: string; activity?: string; started: string }): BackgroundTask {
  return create(BackgroundTaskSchema, {
    id: init.description,
    description: init.description,
    subagentType: init.subagentType ?? "",
    activity: init.activity ?? "",
    started: at(init.started),
  });
}

function rowsOf(container: HTMLElement): HTMLElement[] {
  return [...container.querySelectorAll<HTMLElement>(".plist .row")];
}

test("SidePanel: the card takes the tone class it is given", () =>
  withDom(
    <SidePanel label="Sub-agents" tone="attention" href={href} onClose={() => undefined}>
      <p>body</p>
    </SidePanel>,
    (container) => {
      const card = container.querySelector(".rcard");
      assert.ok(card?.classList.contains("attention"), "the card does not take the attention tone");
      assert.equal(card?.classList.contains("live"), false, "the card takes a tone it was not given");
    },
  ));

test("SidePanel: an agents list and any other child draw in the same frame, one width", async () => {
  const agents = create(BackgroundTaskSchema, { id: "a", description: "Map the rota", started: at("@14:40:00") });
  let agentsFrame = "";
  let otherFrame = "";
  await withDom(
    <SidePanel label="Sub-agents" tone="live" href={href} onClose={() => undefined}>
      <ListPanel kind="agents" tasks={[agents]} now={now} />
    </SidePanel>,
    (container) => {
      agentsFrame = frameOf(container);
    },
  );
  await withDom(
    <SidePanel label="Other" tone="live" href={href} onClose={() => undefined}>
      <p>body</p>
    </SidePanel>,
    (container) => {
      otherFrame = frameOf(container);
    },
  );
  assert.equal(agentsFrame, otherFrame, "the agents list and another child draw in different frames");
});

// frameOf is the classes of the aside and of its card: the frame every thing opens in.
function frameOf(container: HTMLElement): string {
  const aside = container.querySelector("aside");
  const card = container.querySelector(".rcard");
  return `${aside?.getAttribute("class")} | ${card?.getAttribute("class")}`;
}

test("SidePanel: Open in a new window links to the href in a new window", () =>
  withDom(
    <SidePanel label="Sub-agents" tone="live" href={href} onClose={() => undefined}>
      <p>body</p>
    </SidePanel>,
    (container) => {
      const link = container.querySelector<HTMLAnchorElement>('a[aria-label="Open in a new window"]');
      assert.ok(link !== null, "there is no Open in a new window link");
      assert.equal(link.getAttribute("href"), href);
      assert.equal(link.getAttribute("target"), "_blank");
      assert.ok(link.getAttribute("rel")?.includes("noopener"), "the link does not set rel noopener");
    },
  ));

test("SidePanel: Close the panel calls onClose", () => {
  let closed = 0;
  return withDom(
    <SidePanel label="Sub-agents" tone="live" href={href} onClose={() => closed++}>
      <p>body</p>
    </SidePanel>,
    (container) => {
      const button = container.querySelector<HTMLButtonElement>('button[aria-label="Close the panel"]');
      assert.ok(button !== null, "there is no Close the panel button");
      assert.equal(button.getAttribute("title"), "Close  ]");
      button.click();
      assert.equal(closed, 1, "Close did not call onClose once");
    },
  );
});

test("WorkLists: agent rows sit under the Sub-agents head and its lead line, each with its description, activity and clock, and live", async () => {
  const tasks = [
    task({ description: "Map the rota", subagentType: "Explore", activity: "Reading the rota", started: "@14:37:58" }),
    task({ description: "Check the holds", activity: "Running the tests", started: "@14:40:30" }),
  ];
  await withDom(<ListPanel kind="agents" tasks={tasks} now={now} />, (container) => {
    assert.equal(container.querySelector(".plist h2")?.textContent, "Sub-agents");
    assert.equal(container.querySelector(".plist .inote")?.textContent, "These run for this conversation.");
    const rows = rowsOf(container);
    assert.equal(rows.length, 2);
    assert.equal(rows[0].querySelector(".title")?.textContent, "Map the rota");
    assert.equal(rows[0].querySelector(".age")?.textContent, "3:02", "the clock is not read from started");
    assert.equal(rows[0].querySelector(".l2")?.textContent, "Explore Reading the rota");
    assert.equal(rows[1].querySelector(".l2")?.textContent, "agent Running the tests", "an empty sub-agent type is not labelled agent");
    assert.ok(rows.every((row) => row.classList.contains("live")), "an agent row is not live");
    assert.ok(rows.every((row) => !row.classList.contains("quiet")), "an agent row is grey");
  });
});

test("WorkLists: shell rows sit under the Left running head and its lead line, and are grey", async () => {
  const tasks = [task({ description: "Watch the build", activity: "tail -f build.log", started: "@14:37:58" })];
  await withDom(<ListPanel kind="running" tasks={tasks} now={now} />, (container) => {
    assert.equal(container.querySelector(".plist h2")?.textContent, "Left running");
    assert.equal(
      container.querySelector(".plist .inote")?.textContent,
      "Nothing waits on these, and the session can be done while they run.",
    );
    const rows = rowsOf(container);
    assert.equal(rows.length, 1);
    assert.equal(rows[0].querySelector(".title")?.textContent, "Watch the build");
    assert.equal(rows[0].querySelector(".age")?.textContent, "3:02");
    assert.equal(rows[0].querySelector(".l2")?.textContent, "tail -f build.log");
    assert.ok(rows[0].classList.contains("quiet"), "a shell row is not grey");
    assert.ok(!rows[0].classList.contains("live"), "a shell row is live");
  });
});

test("WorkLists: a list drawn without its head keeps its lead line and rows and draws no head", async () => {
  const tasks = [
    task({ description: "Map the rota", subagentType: "Explore", activity: "Reading the rota", started: "@14:37:58" }),
  ];
  await withDom(<ListPanel kind="agents" tasks={tasks} now={now} headless />, (container) => {
    assert.ok(container.querySelector(".plist h2") === null, "the list draws a head the sheet already names");
    assert.equal(container.querySelector(".plist .inote")?.textContent, "These run for this conversation.");
    const rows = rowsOf(container);
    assert.equal(rows.length, 1);
    assert.equal(rows[0].querySelector(".title")?.textContent, "Map the rota");
  });
});

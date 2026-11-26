import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { stepStrip, stripHost } from "./app";
import { Dir } from "./dir";
import { liveEpoch } from "./fixtures/clock";
import { type Epic, EpicSchema, type EpicStep, EpicStepSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import {
  HeadlessSessionSchema,
  HostStatusResponseSchema,
  PaneSchema,
  SessionSchema,
  type HeadlessSession,
  type HostStatusResponse,
  type Pane,
  type Session,
} from "./gen/orrery/v1/orrery_pb";
import { InboxRow, type RowMore } from "./inboxrow";
import { ProjectPage } from "./projectpage";
import { PaneStrip } from "./strip";
import { type Row, rowsFrom } from "./rows";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import { TitleRow } from "./titlerow";
import { buildTree, type Project } from "./tree";

// now is the clock the places read their times against, in unix seconds: the fixtures' live instant, so no test reads the
// wall clock.
const now = Date.parse(liveEpoch) / 1000;

// serving is the host this server runs on; brine is the other host the fixtures draw.
const serving = "halyard";
const noop = () => {};

// noMore is a row's More with no items and no menu open.
const noMore: RowMore = { open: false, items: [], toggle: noop, close: noop };

// handlers are the project page's presses, which do nothing here.
const handlers = { onOpen: noop, onNewHere: noop, onBack: noop };

// status is the serving host's status, with one stat, which no strip draws: a strip names the host alone.
const status: HostStatusResponse = create(HostStatusResponseSchema, {
  host: serving,
  stats: [{ label: "cpu", value: "12%", percent: 12, level: "ok" }],
});

// pane is an invented pane on host, running in cwd ("" for no directory), whose conversation is orreryId.
function pane(id: string, host: string, cwd: string, orreryId = `conv-${id}`): Pane {
  return create(PaneSchema, {
    id,
    host,
    session: "work",
    cwd,
    claudeSession: `claude-${id}`,
    state: "done",
    summary: `pane ${id}`,
    changed: BigInt(now - 60),
    seen: BigInt(now - 60),
    orreryId,
  });
}

// headless is an invented headless session on host, running in dir ("" for no directory).
function headless(id: string, host: string, dir: string): HeadlessSession {
  return create(HeadlessSessionSchema, {
    id,
    host,
    dir,
    headline: `session ${id}`,
    state: "done",
    changed: BigInt(now - 60),
    seen: BigInt(now - 60),
  });
}

// session is an invented Sessions.List session, which names no pane and carries no host.
function session(id: string): Session {
  return create(SessionSchema, { id, state: "done", summary: `session ${id}`, changed: String(now - 60), seen: String(now - 60) });
}

// stepOn is an invented running step, on host, of the epic beacon.
function stepOn(id: string, host: string): EpicStep {
  return create(EpicStepSchema, {
    id,
    purpose: id,
    host,
    agent: "builder",
    end: "",
    doing: "",
    started: BigInt(now - 120),
    stageStarted: BigInt(now - 60),
  });
}

// epicOn is an invented live epic on host, in /srv/app, steered by the conversation steering, whose running steps are steps.
function epicOn(host: string, steering: string, steps: EpicStep[]): Epic {
  return create(EpicSchema, {
    id: "harbour/beacon",
    repository: "harbour",
    slug: "beacon",
    source: "orreryd",
    live: true,
    phaseAt: BigInt(now - 600),
    changed: BigInt(now - 60),
    host,
    path: "/srv/app",
    steering,
    runningSteps: steps,
  });
}

// detailOf is the epic's Get answer: the epic with its steps.
function detailOf(listed: Epic): EpicsGetResponse {
  return create(EpicsGetResponseSchema, { epic: listed, steps: listed.runningSteps });
}

// treeOf places the rows and epics in the inbox at the serving host, as the app does.
function treeOf(rows: Row[], epics: Epic[] = []): Project[] {
  const details = new Map(epics.map((epic) => [epic.id, detailOf(epic)]));
  return buildTree({ rows, epics, details, contracts: [], host: serving, view: "inbox" });
}

// projectAt is the project with the key, which the tree must draw.
function projectAt(projects: Project[], key: string): Project {
  const project = projects.find((candidate) => candidate.key === key);
  assert.ok(project !== undefined, `the tree draws a project keyed ${key}`);
  return project;
}

// onlyRow is the one row a list holds.
function onlyRow(rows: Row[]): Row {
  assert.equal(rows.length, 1, "the list holds one row");
  const [row] = rows;
  assert.ok(row !== undefined, "the list holds one row");
  return row;
}

// cardOf draws a project's card as the inbox does: each conversation of the project as its inbox row.
function cardOf(project: Project): JSX.Element {
  return (
    <Dir key={project.key} project={project} folded={false} waiting={0} endedOpen={false} onFold={noop} onEnded={noop} ended={null}>
      {project.convos.map((convo) =>
        convo.row === null ? null : (
          <InboxRow key={convo.key} row={convo.row} now={now} cursor={false} more={noMore} onOpen={noop} onSeen={noop} />
        ),
      )}
    </Dir>
  );
}

// withDrawn draws element in a fresh DOM, runs check on the container, and removes the DOM again.
async function withDrawn(element: JSX.Element, check: (container: HTMLElement) => void | Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    await act(() => {
      render(element, container);
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

// hostNames lists the names of the host labels under root that the selector picks, in order.
function hostNames(root: ParentNode, selector: string): string[] {
  return [...root.querySelectorAll(selector)].map((label) => label.querySelector(".name")?.textContent ?? "");
}

// one returns the first element under root that matches selector, which the drawing must hold.
function one(root: ParentNode, selector: string): Element {
  const found = root.querySelector(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// rowElement returns the drawn row keyed key, which the drawing must hold.
function rowElement(container: ParentNode, key: string): Element {
  return one(container, `.row[data-key="${key}"]`);
}

// The fixtures: a directory on the serving host and two on brine, a pane and a headless session in each, a pane and a
// headless session with no directory on either host, and a Sessions.List session, which carries no host.
const panes = [
  pane("%1", serving, "/srv/app"),
  pane("%2", "brine", "/srv/app"),
  pane("%3", "brine", "/home/owner/code/lamp"),
  pane("%4", serving, ""),
];
const rows = rowsFrom(panes, [session("sess-4")], [headless("sess-1", "brine", "/srv/app"), headless("sess-2", serving, ""), headless("sess-3", "brine", "")]);

// The card each project key holds, and the host a card's head names.
const headingByCard: Record<string, string[]> = {
  "|/srv/app": [serving],
  "brine|/srv/app": ["brine"],
  "brine|/home/owner/code/lamp": ["brine"],
  nodir: [],
};

// The host each row names inline, keyed by the row's key: a row under a card names none, and a row with no directory
// names its own host, the serving host's included.
const inlineByRow: Record<string, string[]> = {
  "pane:halyard:%1": [],
  "pane:brine:%2": [],
  "pane:brine:%3": [],
  "pane:halyard:%4": [serving],
  "session:sess-1": [],
  "session:sess-2": [serving],
  "session:sess-3": ["brine"],
  "session:sess-4": [],
};

test("HostsWebHostLabel: each card names its host once as a heading, on the serving host and on another, and the card with no directory names none", async () => {
  const projects = treeOf(rows);
  assert.deepEqual(projects.map((project) => project.key).sort(), Object.keys(headingByCard).sort(), "the tree draws four cards");
  await withDrawn(<>{projects.map(cardOf)}</>, (container) => {
    const cards = [...container.querySelectorAll("section.dir")];
    assert.equal(cards.length, projects.length, "each project draws its card");
    projects.forEach((project, index) => {
      const head = one(cards[index] ?? container, ".dir-head");
      assert.deepEqual(hostNames(head, "span.host-label.heading"), headingByCard[project.key], `the ${project.key} card heads its host`);
    });
  });
});

test("HostsWebHostLabel: the rows under a card name no host, and a row with no directory names its own host inline, the serving host's included", async () => {
  await withDrawn(<>{treeOf(rows).map(cardOf)}</>, (container) => {
    const drawn = [...container.querySelectorAll(".row[data-key]")].map((row) => row.getAttribute("data-key") ?? "");
    assert.deepEqual(drawn.sort(), Object.keys(inlineByRow).sort(), "every row is drawn once");
    for (const key of drawn) {
      assert.deepEqual(hostNames(rowElement(container, key), "span.host-label"), inlineByRow[key], `the row ${key} names its host as it should`);
    }
  });
});

test("HostsWebHostLabel: a headless session keeps the host it runs on, and its title row's strip names that host", async () => {
  const onBrine = onlyRow(rowsFrom([], [], [headless("sess-1", "brine", "/srv/app")]));
  assert.equal(onBrine.host, "brine", "the headless session's row keeps its host");
  await withDrawn(<PaneStrip host={stripHost(onBrine, status)} band />, (container) => {
    assert.deepEqual(hostNames(container, ".pane-strip-host .host-label"), ["brine"], "the strip names the row's host");
    assertNone(container.querySelector(".meter"), "a row on another host draws no stats");
  });
  const onServing = onlyRow(rowsFrom([], [], [headless("sess-2", serving, "/srv/app")]));
  await withDrawn(<PaneStrip host={stripHost(onServing, status)} band />, (container) => {
    assert.deepEqual(hostNames(container, ".pane-strip-host .host-label"), [serving], "the strip names the serving host");
    assertNone(container.querySelector(".meter"), "a row on the serving host draws no meters");
  });
});

test("HostsWebHostLabel: a pane's title row names the pane's own host, on another host and on the serving host", async () => {
  const onBrine = onlyRow(rowsFrom([pane("%2", "brine", "/srv/app")], [], []));
  const onServing = onlyRow(rowsFrom([pane("%1", serving, "/srv/app")], [], []));
  await withDrawn(<PaneStrip host={stripHost(onBrine, status)} band />, (container) => {
    assert.deepEqual(hostNames(container, ".pane-strip-host .host-label"), ["brine"], "a pane on brine names brine");
  });
  await withDrawn(<PaneStrip host={stripHost(onServing, status)} band />, (container) => {
    assert.deepEqual(hostNames(container, ".pane-strip-host .host-label"), [serving], "a pane on the serving host names it");
  });
});

test("HostsWebHostLabel: a project page's head names its host once, as a label, on the serving host and on another", async () => {
  const projects = treeOf(rows);
  await withDrawn(<ProjectPage project={projectAt(projects, "|/srv/app")} now={now} narrow={false} {...handlers} />, (container) => {
    assert.deepEqual(hostNames(one(container, ".proj-head"), "span.host-label"), [serving], "the serving host's page names it once");
  });
  await withDrawn(<ProjectPage project={projectAt(projects, "brine|/srv/app")} now={now} narrow={false} {...handlers} />, (container) => {
    assert.deepEqual(hostNames(one(container, ".proj-head"), "span.host-label"), ["brine"], "brine's page names it once");
  });
});

test("HostsWebHostLabel: with one host every place still names it: each card, each row with no directory, the page and the strip", async () => {
  const solo = rowsFrom(
    [pane("%1", serving, "/srv/app"), pane("%2", serving, "")],
    [],
    [headless("sess-1", serving, "/srv/app"), headless("sess-2", serving, "")],
  );
  const projects = treeOf(solo);
  assert.deepEqual(
    new Set(projects.map((project) => project.key)),
    new Set(["|/srv/app", "nodir"]),
    "the tree draws the directory and the card with no directory",
  );
  await withDrawn(<>{projects.map(cardOf)}</>, (container) => {
    const cards = [...container.querySelectorAll("section.dir")];
    projects.forEach((project, index) => {
      const head = one(cards[index] ?? container, ".dir-head");
      assert.deepEqual(hostNames(head, "span.host-label.heading"), headingByCard[project.key], `the one host's ${project.key} card`);
    });
    assert.deepEqual(hostNames(rowElement(container, "pane:halyard:%2"), "span.host-label"), [serving], "the pane with no directory names the host inline");
    assert.deepEqual(hostNames(rowElement(container, "session:sess-2"), "span.host-label"), [serving], "the headless session with no directory names it inline");
  });
  await withDrawn(<ProjectPage project={projectAt(projects, "|/srv/app")} now={now} narrow={false} {...handlers} />, (container) => {
    assert.deepEqual(hostNames(one(container, ".proj-head"), "span.host-label"), [serving], "the page names the one host");
  });
  await withDrawn(<PaneStrip host={stripHost(onlyRow(rowsFrom([pane("%1", serving, "/srv/app")], [], [])), status)} band />, (container) => {
    assert.deepEqual(hostNames(container, ".pane-strip-host .host-label"), [serving], "the strip names the one host");
  });
});

test("HostsWebHostLabel: an epic names its host once in its card on the page, and each step names its own host in its title row, on two hosts", async () => {
  const lamp = stepOn("lamp", "sextant");
  const bell = stepOn("bell", "lantern");
  const projects = treeOf(rowsFrom([pane("%1", serving, "/srv/app", "conv-1")], [], []), [epicOn("sextant", "conv-1", [lamp, bell])]);
  await withDrawn(<ProjectPage project={projectAt(projects, "|/srv/app")} now={now} narrow={false} {...handlers} />, (container) => {
    assert.deepEqual(hostNames(one(container, ".proj-head"), "span.host-label"), [serving], "the page names its host once");
    assert.deepEqual(hostNames(one(container, ".wcard"), "span.host-label"), ["sextant"], "the epic's card names its epic's host once");
  });
  await withDrawn(<TitleRow crumbs={[{ label: "beacon" }, { label: "bell" }]} strip={stepStrip(bell)} />, (container) => {
    assert.deepEqual(hostNames(one(container, ".title-row"), "span.host-label"), ["lantern"], "the bell step's title row names its host");
  });
  await withDrawn(<TitleRow crumbs={[{ label: "beacon" }, { label: "lamp" }]} strip={stepStrip(lamp)} />, (container) => {
    assert.deepEqual(hostNames(one(container, ".title-row"), "span.host-label"), ["sextant"], "the lamp step's title row names its host");
  });
});

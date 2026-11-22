import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { EpicCalls } from "./card";
import { EpicWidget } from "./card";
import { EpicRow } from "./epicrow";
import { blockOf, type EpicsView, epicsView } from "./epics";
import { liveEpoch } from "./fixtures/clock";
import * as glyphs from "./glyphs";
import {
  type Epic,
  EpicRequirementSchema,
  EpicSchema,
  type EpicsGetResponse,
  EpicsGetResponseSchema,
} from "./gen/orrery/v1/epics_pb";
import { Notice } from "./notices";
import { assertNone, installTestDom, removeTestDom } from "./testdom";
import type { EpicNode } from "./tree";
import { workOf } from "./work";
import { WorkRail } from "./workrail";
import { WorkCard, workCardOf } from "./workcard";

// Each test's name carries the token EpicMark: the mark draws in each place the Gauge drew, each place drawn on its own,
// outside any page.

const epicId = "orrery/mark-places";
const session = "sess-mark-places";
// now is the clock the places read their times against, in unix seconds: the fixtures' live instant.
const now = Date.parse(liveEpoch) / 1000;

// listedEpic is the epic's Epics.List row: live, accepted, and steered by the session the conversation names.
function listedEpic(): Epic {
  return create(EpicSchema, {
    id: epicId,
    repository: "orrery",
    slug: "mark-places",
    outcome: "accepted",
    live: true,
    steering: session,
  });
}

// detailOf is the epic's Epics.Get answer: its centre running, with one requirement proven, one in flight and one not
// reached.
function detailOf(epic: Epic): EpicsGetResponse {
  return create(EpicsGetResponseSchema, {
    epic,
    centre: "running",
    requirements: [
      create(EpicRequirementSchema, { id: "R1", state: "proven" }),
      create(EpicRequirementSchema, { id: "R2", state: "in_flight" }),
      create(EpicRequirementSchema, { id: "R3", state: "not_reached" }),
    ],
  });
}

// Places are the containers each place is drawn into, by the place's name.
interface Places {
  row: HTMLElement;
  card: HTMLElement;
  workCard: HTMLElement;
  notice: HTMLElement;
  rail: HTMLElement;
}

// drawAll draws every place that draws the epic's mark, each into its own container of one fresh DOM, runs check on the
// containers, and removes the DOM again.
async function drawAll(check: (places: Places) => void): Promise<void> {
  const window = installTestDom();
  const places: Places = {
    row: document.createElement("div"),
    card: document.createElement("div"),
    workCard: document.createElement("div"),
    notice: document.createElement("div"),
    rail: document.createElement("div"),
  };
  const epic = listedEpic();
  const detail = detailOf(epic);
  const node: EpicNode = { kind: "epic", key: `epic:${epicId}`, block: blockOf(epic, detail), listed: epic, contract: null };
  const epics: EpicsView = epicsView([epic], new Map([[epicId, detail]]));
  const calls: EpicCalls = {
    get: async () => detail,
    answer: async () => "",
    openEpic: () => {},
    openStep: () => {},
    stepResult: async () => "",
    now,
    epics,
  };
  const groups = workOf({ session, pane: false, background: [] }, epics, now);
  try {
    await act(() => {
      render(<EpicRow hung={node} now={now} current={false} onSelect={() => {}} />, places.row);
      render(<EpicWidget epic={epicId} calls={calls} />, places.card);
      render(<WorkCard card={workCardOf(node, now)} onOpen={() => {}} />, places.workCard);
      render(
        <Notice
          head={{ lead: { kind: "mark", detail }, dir: "", what: "mark-places asks you a question" }}
          when="2m"
          body="a body"
        />,
        places.notice,
      );
      render(<WorkRail groups={groups} lit="" layout="rail" onOpen={() => {}} />, places.rail);
    });
    check(places);
  } finally {
    await act(() => {
      for (const container of Object.values(places)) {
        render(null, container);
      }
    });
    await removeTestDom(window);
  }
}

// one returns the first element under root that matches selector, which the drawing must hold.
function one(root: ParentNode, selector: string): Element {
  const found = root.querySelector(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

test("EpicMark places: the inbox row draws the mark at 32", () =>
  drawAll((places) => {
    const mark = one(places.row, ".row.epic svg.epicmark");
    assert.equal(mark.getAttribute("data-size"), "32");
    assert.equal(mark.getAttribute("aria-label"), "1 of 3 requirements proven, running");
  }));

test("EpicMark places: the card, the work card, the notices and the work rail draw the mark at 24", () =>
  drawAll((places) => {
    const drawn: [string, HTMLElement][] = [
      ["the card", places.card],
      ["the work card", places.workCard],
      ["the notices", places.notice],
      ["the work rail", places.rail],
    ];
    for (const [place, container] of drawn) {
      assert.equal(one(container, "svg.epicmark").getAttribute("data-size"), "24", `${place} draws the mark at 24`);
    }
  }));

test("EpicMark places: nothing draws a Gauge, and every place draws the mark in its stead", () =>
  drawAll((places) => {
    assert.equal(Object.hasOwn(glyphs, "Gauge"), false, "the glyphs export no Gauge");
    for (const [place, container] of Object.entries(places)) {
      assertNone(container.querySelector(".gauge"), `${place} draws a Gauge`);
      one(container, "svg.epicmark");
    }
  }));

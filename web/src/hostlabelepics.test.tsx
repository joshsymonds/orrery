import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { stepStrip } from "./app";
import { type EpicCommands, EpicPage } from "./epicpage";
import { EpicRow } from "./epicrow";
import { blockOf } from "./epics";
import type { LoadArtifact } from "./evidence";
import { liveEpoch } from "./fixtures/clock";
import { type Epic, EpicSchema, type EpicStep, EpicStepSchema, type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { NowSection } from "./nowsection";
import { ContextBand, EpicPin } from "./taskband";
import { installTestDom, removeTestDom } from "./testdom";
import { TitleRow } from "./titlerow";
import type { EpicNode } from "./tree";
import { WorkCard, workCardOf } from "./workcard";

// now is the clock the places read their times against, in unix seconds: the fixtures' live instant, so no test reads the
// wall clock.
const now = Date.parse(liveEpoch) / 1000;

const noop = () => {};

// noArtifact reads no artifact: no place here opens a log.
const noArtifact: LoadArtifact = async () => {
  throw new Error("no artifact is read in this test");
};

// noCommands are the epic page's commands, each of which does nothing: no place here sends one.
const noCommands: EpicCommands = {
  pause: noop,
  resume: noop,
  stop: noop,
  retry: noop,
  answer: async () => "",
  release: async () => ({ kind: "landed" }),
  close: async () => ({ kind: "landed" }),
  reread: async () => true,
};

// step is an invented running step of the epic beacon, on host, labelled by its purpose.
function step(id: string, host: string, purpose: string): EpicStep {
  return create(EpicStepSchema, {
    id,
    purpose,
    host,
    agent: "builder",
    end: "",
    doing: `${purpose} is under way`,
    started: BigInt(now - 120),
    stageStarted: BigInt(now - 60),
  });
}

// epicOn is an invented live epic, beacon, on host, whose running steps are steps.
function epicOn(host: string, steps: EpicStep[]): Epic {
  return create(EpicSchema, {
    id: "harbour/beacon",
    repository: "harbour",
    slug: "beacon",
    source: "orreryd",
    live: true,
    phaseAt: BigInt(now - 600),
    host,
    runningSteps: steps,
  });
}

// detailOf is the epic's Get answer: the epic with its steps, each of which is also one of its running steps.
function detailOf(listed: Epic): EpicsGetResponse {
  return create(EpicsGetResponseSchema, { epic: listed, steps: listed.runningSteps });
}

// epicNode hangs the listed epic under its conversation as the tree does, with its block read from its detail.
function epicNode(listed: Epic, detail: EpicsGetResponse | undefined = undefined): EpicNode {
  return { kind: "epic", key: `epic:${listed.id}`, block: blockOf(listed, detail), listed, contract: null };
}

// sectionProps are the Now section's props for the detail, with no-op actions.
function sectionProps(detail: EpicsGetResponse): Parameters<typeof NowSection>[0] {
  return {
    detail,
    now,
    narrow: false,
    numberKeys: false,
    onAnswer: async () => "",
    onRetry: noop,
    onStop: noop,
    onResume: noop,
  };
}

// pageProps are the epic page's props for the detail, with no-op actions.
function pageProps(detail: EpicsGetResponse): Parameters<typeof EpicPage>[0] {
  return {
    detail,
    history: [],
    now,
    loadArtifact: noArtifact,
    commands: noCommands,
    numberKeys: false,
    commandError: "",
    onOpenContract: noop,
    onOpenSession: noop,
  };
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

// labelsIn lists the names of the HostLabels drawn under root, in order. A label drawn as a card heading fails: each place
// draws its host inline.
function labelsIn(root: ParentNode): string[] {
  return [...root.querySelectorAll("span.host-label")].map((label) => {
    assert.equal(label.classList.contains("heading"), false, "the label is drawn inline, not as a card heading");
    return label.querySelector(".name")?.textContent ?? "";
  });
}

// one returns the first element under root that matches selector, which the drawing must hold.
function one(root: ParentNode, selector: string): Element {
  const found = root.querySelector(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// split is an epic on sextant whose two running steps are on sextant and on lantern.
const lamp = step("lamp", "sextant", "lamp");
const bell = step("bell", "lantern", "bell");
const split = epicOn("sextant", [lamp, bell]);

test("HostsWebHostLabel epics: an EpicRow names its epic's host once, inline, and each step row its own host, on two hosts", () =>
  withDrawn(<EpicRow hung={epicNode(split)} now={now} current={false} onSelect={noop} />, (container) => {
    assert.deepEqual(labelsIn(one(container, ".row.epic")), ["sextant"], "the epic's row names its host once");
    assert.deepEqual(
      [...container.querySelectorAll(".row.step")].map((row) => labelsIn(row)),
      [["sextant"], ["lantern"]],
      "each step row names the host its step runs on",
    );
  }));

test("HostsWebHostLabel epics: with one host an EpicRow and each of its step rows still name it", () => {
  const solo = epicOn("sextant", [step("lamp", "sextant", "lamp"), step("horn", "sextant", "horn")]);
  return withDrawn(<EpicRow hung={epicNode(solo)} now={now} current={false} onSelect={noop} />, (container) => {
    assert.deepEqual(labelsIn(one(container, ".row.epic")), ["sextant"], "the epic's row names the one host");
    assert.deepEqual(
      [...container.querySelectorAll(".row.step")].map((row) => labelsIn(row)),
      [["sextant"], ["sextant"]],
      "each step row names the one host",
    );
  });
});

test("HostsWebHostLabel epics: a Now table row names its step's host inline, on two hosts and with one", async () => {
  await withDrawn(<NowSection {...sectionProps(detailOf(split))} />, (container) => {
    assert.deepEqual(
      [...container.querySelectorAll(".tbl.steps .tr")].map((row) => labelsIn(row)),
      [["sextant"], ["lantern"]],
      "each Now row names the host its step runs on",
    );
  });
  const solo = epicOn("sextant", [step("lamp", "sextant", "lamp")]);
  await withDrawn(<NowSection {...sectionProps(detailOf(solo))} />, (container) => {
    assert.deepEqual(
      [...container.querySelectorAll(".tbl.steps .tr")].map((row) => labelsIn(row)),
      [["sextant"]],
      "the one Now row names the one host",
    );
  });
});

test("HostsWebHostLabel epics: a WorkCard names its epic's host once, inline, in its foot", () =>
  withDrawn(<WorkCard card={workCardOf(epicNode(split), now)} onOpen={noop} />, (container) => {
    assert.deepEqual(labelsIn(one(container, ".wcard")), ["sextant"], "the card names its epic's host once");
    assert.deepEqual(labelsIn(one(container, ".wcard .foot")), ["sextant"], "the host is in the card's foot");
  }));

test("HostsWebHostLabel epics: an EpicPin names its epic's host once, inline, and the ContextBand names it once in its pin", async () => {
  await withDrawn(<EpicPin detail={detailOf(split)} now={now} onOpen={noop} />, (container) => {
    assert.deepEqual(labelsIn(one(container, "button.epic-pin")), ["sextant"], "the pin names its epic's host once");
  });
  await withDrawn(
    <ContextBand detail={detailOf(split)} now={now} current="" onOpenEpic={noop} onOpenTask={noop} />,
    (container) => {
      assert.deepEqual(labelsIn(container), ["sextant"], "the band names its epic's host once, in its pin");
    },
  );
});

test("HostsWebHostLabel epics: an epic page's title row names the epic's host once, inline, on the desk and on the phone", async () => {
  for (const narrow of [false, true]) {
    await withDrawn(<EpicPage {...pageProps(detailOf(split))} narrow={narrow} />, (container) => {
      assert.deepEqual(labelsIn(one(container, ".title-row")), ["sextant"], `the ${narrow ? "phone" : "desk"} title row`);
    });
  }
});

test("HostsWebHostLabel epics: a step's title row names the host its step runs on, inline, and a step with no host names none", async () => {
  const crumbs = [{ label: "beacon › builder · step · round 1" }, { label: "bell" }];
  await withDrawn(<TitleRow crumbs={crumbs} strip={stepStrip(bell)} />, (container) => {
    assert.deepEqual(labelsIn(one(container, ".title-row")), ["lantern"], "the step's title row names its host");
  });
  await withDrawn(<TitleRow crumbs={crumbs} strip={stepStrip(step("quiet", "", "quiet"))} />, (container) => {
    assert.deepEqual(labelsIn(one(container, ".title-row")), [], "a step with no host names none");
  });
});

test("HostsWebHostLabel epics: an empty host draws no label where a host beside it draws one, in the row, the card, the pin and the title row", async () => {
  const hosted = epicOn("sextant", [step("lamp", "sextant", "lamp")]);
  const unhosted = epicOn("", [step("lamp", "", "lamp")]);
  await withDrawn(<EpicRow hung={epicNode(hosted)} now={now} current={false} onSelect={noop} />, (container) => {
    assert.deepEqual(labelsIn(one(container, ".row.epic")), ["sextant"], "the hosted row names its host");
  });
  await withDrawn(<EpicRow hung={epicNode(unhosted)} now={now} current={false} onSelect={noop} />, (container) => {
    assert.deepEqual(labelsIn(container), [], "the unhosted row and its step name no host");
  });
  await withDrawn(<WorkCard card={workCardOf(epicNode(hosted), now)} onOpen={noop} />, (container) => {
    assert.deepEqual(labelsIn(container), ["sextant"], "the hosted card names its host");
  });
  await withDrawn(<WorkCard card={workCardOf(epicNode(unhosted), now)} onOpen={noop} />, (container) => {
    assert.deepEqual(labelsIn(container), [], "the unhosted card names no host");
  });
  await withDrawn(<EpicPin detail={detailOf(hosted)} now={now} onOpen={noop} />, (container) => {
    assert.deepEqual(labelsIn(container), ["sextant"], "the hosted pin names its host");
  });
  await withDrawn(<EpicPin detail={detailOf(unhosted)} now={now} onOpen={noop} />, (container) => {
    assert.deepEqual(labelsIn(container), [], "the unhosted pin names no host");
  });
  await withDrawn(<EpicPage {...pageProps(detailOf(hosted))} />, (container) => {
    assert.deepEqual(labelsIn(one(container, ".title-row")), ["sextant"], "the hosted title row names its host");
  });
  await withDrawn(<EpicPage {...pageProps(detailOf(unhosted))} />, (container) => {
    assert.deepEqual(labelsIn(one(container, ".title-row")), [], "the unhosted title row names no host");
  });
});

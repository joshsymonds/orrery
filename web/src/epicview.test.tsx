import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";

import { EpicView } from "./epicview";
import { EpicsGetResponseSchema, type EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { installTestDom, removeTestDom } from "./testdom";

function detail(init: MessageInitShape<typeof EpicsGetResponseSchema>): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

const epic = { id: "e1", slug: "demo-epic", repository: "demo-repo", outcome: "ship the demo" };

const tasks = [
  { slug: "1-alpha", status: "integrated" },
  { slug: "2-beta", status: "gated" },
  { slug: "3-gamma", status: "done at 3f9c2e1" },
];

const steps = [
  {
    id: "s1",
    task: "1-alpha",
    agent: "scout",
    started: 100n,
    end: "done",
    checks: [
      { round: 1, name: "unit", ok: true },
      { round: 1, name: "lint", ok: false },
    ],
  },
  { id: "s2", task: "2-beta", agent: "builder", started: 200n, held: true },
  { id: "s3", agent: "reviewer", started: 300n },
];

async function withView(
  props: { detail: EpicsGetResponse; cursorStep: string },
  check: (container: HTMLElement) => void,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<EpicView {...props} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

function classOf(element: Element): string | null {
  return element.getAttribute("class");
}

test("one ring per task, first task innermost, classed by its state and titled with its status", () =>
  withView({ detail: detail({ epic, tasks }), cursorStep: "" }, (container) => {
    const rings = [...container.querySelectorAll("circle.ring")];
    assert.equal(rings.length, 3);
    assert.deepEqual(
      rings.map((ring) => classOf(ring)),
      ["ring ts-done", "ring ts-gated", "ring ts-unknown"],
    );
    assert.deepEqual(
      rings.map((ring) => ring.querySelector("title")?.textContent),
      ["1-alpha: integrated", "2-beta: gated", "3-gamma: done at 3f9c2e1"],
    );
    const radii = rings.map((ring) => Number(ring.getAttribute("r")));
    assert.ok(radii[0] < radii[1] && radii[1] < radii[2], `radii were ${radii}`);
  }));

test("the task list shows each status as recorded beside its state mark", () =>
  withView({ detail: detail({ epic, tasks }), cursorStep: "" }, (container) => {
    const rows = [...container.querySelectorAll(".task")];
    assert.equal(rows.length, 3);
    assert.ok(rows[0].querySelector(".mark.ts-done"), "integrated draws done");
    assert.ok(rows[1].querySelector(".mark.ts-gated"), "gated draws gated");
    assert.ok(rows[2].querySelector(".mark.ts-unknown"), "unknown text draws neutral");
    assert.ok(rows[2].textContent?.includes("3-gamma"));
    assert.ok(rows[2].textContent?.includes("done at 3f9c2e1"));
  }));

test("one pip per stored check, ok or bad, titled with round, name and outcome", () =>
  withView({ detail: detail({ epic, tasks, steps }), cursorStep: "" }, (container) => {
    const first = container.querySelectorAll(".step")[0];
    assert.equal(first.querySelectorAll(".pip").length, 2);
    const ok = first.querySelector(".pip.ok");
    const bad = first.querySelector(".pip.bad");
    assert.ok(ok, "a passed check draws pip ok");
    assert.ok(bad, "a failed check draws pip bad");
    assert.equal(classOf(ok), "pip ok");
    assert.equal(classOf(bad), "pip bad");
    assert.equal(ok.getAttribute("title"), "round 1 unit: passed");
    assert.equal(bad.getAttribute("title"), "round 1 lint: failed");
  }));

test("each step ends with its end kind: the end, else held, else running", () =>
  withView({ detail: detail({ epic, tasks, steps }), cursorStep: "" }, (container) => {
    const ends = [...container.querySelectorAll(".step .end")].map((end) => end.textContent);
    assert.deepEqual(ends, ["done", "held", "running"]);
  }));

test("an ended step shows its end even when it is held", () =>
  withView(
    {
      detail: detail({ epic, steps: [{ id: "s1", task: "1-alpha", end: "stopped", held: true, started: 1n }] }),
      cursorStep: "",
    },
    (container) => {
      assert.equal(container.querySelector(".step .end")?.textContent, "stopped");
    },
  ));

test("a step is labelled by its task, else by its agent", () =>
  withView({ detail: detail({ epic, tasks, steps }), cursorStep: "" }, (container) => {
    const labels = [...container.querySelectorAll(".step .label")].map((label) => label.textContent);
    assert.deepEqual(labels, ["1-alpha", "2-beta", "reviewer"]);
  }));

test("the cursor's step and its task's ring are marked, and nothing else", () =>
  withView({ detail: detail({ epic, tasks, steps }), cursorStep: "s2" }, (container) => {
    const marked = [...container.querySelectorAll(".cur")];
    assert.equal(marked.length, 2);
    const stepRow = container.querySelectorAll(".step")[1];
    assert.equal(classOf(stepRow), "step cur");
    assert.equal(stepRow.getAttribute("aria-current"), "true");
    const rings = [...container.querySelectorAll("circle.ring")];
    assert.equal(classOf(rings[1]), "ring ts-gated cur");
    assert.equal(container.querySelectorAll("[aria-current]").length, 1);
  }));

test("an empty cursorStep marks no step and no ring", () =>
  withView({ detail: detail({ epic, tasks, steps }), cursorStep: "" }, (container) => {
    assert.equal(container.querySelectorAll(".cur").length, 0);
    assert.equal(container.querySelectorAll("[aria-current]").length, 0);
  }));

test("the header draws the slug, repository, outcome and problem", () =>
  withView(
    { detail: detail({ epic: { ...epic, problem: "lock held" } }), cursorStep: "" },
    (container) => {
      assert.equal(container.querySelector(".epic-head .slug")?.textContent, "demo-epic");
      assert.equal(container.querySelector(".epic-head .repo")?.textContent, "demo-repo");
      assert.equal(container.querySelector(".epic-head .outcome")?.textContent, "ship the demo");
      assert.equal(container.querySelector(".epic-head .problem")?.textContent, "lock held");
    },
  ));

test("no problem element when the epic has no problem", () =>
  withView({ detail: detail({ epic }), cursorStep: "" }, (container) => {
    assert.equal(container.querySelector(".problem"), null);
  }));

test("markup in slugs, statuses and labels draws as text with no element", () =>
  withView(
    {
      detail: detail({
        epic,
        tasks: [{ slug: "<img src=x onerror=alert(1)>", status: "<b>done</b>" }],
        steps: [{ id: "s1", task: "<script>alert(1)</script>", started: 1n }],
      }),
      cursorStep: "",
    },
    (container) => {
      assert.equal(container.querySelector("img"), null);
      assert.equal(container.querySelector("script"), null);
      assert.equal(container.querySelector("b"), null);
      assert.ok(container.textContent?.includes("<img src=x onerror=alert(1)>"));
      assert.ok(container.textContent?.includes("<b>done</b>"));
      assert.ok(container.textContent?.includes("<script>alert(1)</script>"));
    },
  ));

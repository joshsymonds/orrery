import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";

import { Handover } from "./handover";
import { type EpicsGetResponse, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { installTestDom, removeTestDom } from "./testdom";

function detail(init: MessageInitShape<typeof EpicsGetResponseSchema>): EpicsGetResponse {
  return create(EpicsGetResponseSchema, init);
}

async function withHandover(
  init: MessageInitShape<typeof EpicsGetResponseSchema>,
  check: (container: HTMLElement) => void,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<Handover detail={detail(init)} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// sectionOf is the section whose eyebrow reads title, or null when the view draws no such section.
function sectionOf(container: HTMLElement, title: string): Element | null {
  return (
    [...container.querySelectorAll("section")].find(
      (section) => section.querySelector("h3.eyebrow")?.firstChild?.textContent === title,
    ) ?? null
  );
}

// itemsOf are a section's list items, in order.
function itemsOf(section: Element | null): Element[] {
  return [...(section?.querySelector("ol")?.children ?? [])];
}

// spansOf are the texts of an element's own spans, in order.
function spansOf(element: Element): (string | null)[] {
  return [...element.children].filter((child) => child.tagName === "SPAN").map((span) => span.textContent);
}

// spanClassesOf are the classes of an element's own spans, in order; a span with no class gives null.
function spanClassesOf(element: Element): (string | null)[] {
  return [...element.children]
    .filter((child) => child.tagName === "SPAN")
    .map((span) => span.getAttribute("class"));
}

// paragraphsOf are the texts of an element's own paragraphs, in order.
function paragraphsOf(element: Element): (string | null)[] {
  return [...element.children].filter((child) => child.tagName === "P").map((paragraph) => paragraph.textContent);
}

// nestedItemsOf are the items of an element's own nested lists, one array per list, in order.
function nestedItemsOf(element: Element): Element[][] {
  return [...element.children].filter((child) => child.tagName === "UL").map((list) => [...list.children]);
}

const epic = { id: "e1", slug: "demo-epic", repository: "demo-repo", outcome: "ended_with_gaps" };

test("an epic with no reviews, ledger findings or release runs draws nothing", () =>
  withHandover({ epic }, (container) => {
    assert.equal(container.textContent, "");
    assert.equal(container.querySelectorAll("*").length, 0);
  }));

test("an epic that ended with gaps draws its phase, its verdicts, and its ledger with closures", () =>
  withHandover(
    {
      epic,
      reviews: [
        { position: 1n, reviewer: "security", step: "rv-1", verdict: "gap", reason: "the token is logged" },
        { position: 2n, reviewer: "tests", step: "rv-2", verdict: "holds", reason: "" },
      ],
      ledger: [
        {
          id: "security-1",
          position: 1n,
          reviewer: "security",
          claim: "the token is logged",
          citation: "a.go:3",
          location: "internal/auth/log.go:41",
          evidence: "the log line prints it",
          step: "vf-1",
          result: "confirmed",
          unverified: false,
          reason: "the log line prints it",
        },
        {
          id: "tests-1",
          position: 2n,
          reviewer: "tests",
          claim: "<b>untested</b> claim",
          citation: "",
          location: "",
          evidence: "",
          step: "",
          result: "",
          unverified: true,
          reason: "no verifier ran",
          closures: [
            { round: 1n, step: "cl-1", result: "open", reason: "" },
            { round: 2n, step: "cl-2", result: "closed", reason: "fixed in a2" },
          ],
        },
      ],
    },
    (container) => {
      const handover = container.querySelector("div.handover");
      if (handover === null) {
        assert.fail("the view drew no div.handover");
      }
      assert.deepEqual(
        [...handover.children].map((child) => child.tagName),
        ["P", "SECTION", "SECTION"],
        "the phase, then REVIEW and LEDGER; RELEASE is absent with no release runs",
      );
      assert.deepEqual(paragraphsOf(handover)[0], "phase ended_with_gaps");

      const review = sectionOf(container, "REVIEW");
      assert.equal(review?.querySelector("h3.eyebrow > span.sub")?.textContent, "2");
      const verdicts = itemsOf(review);
      assert.equal(verdicts.length, 2);
      assert.deepEqual(spansOf(verdicts[0]), ["security", "gap"]);
      assert.deepEqual(spanClassesOf(verdicts[0]), ["path", "state"]);
      assert.deepEqual(paragraphsOf(verdicts[0]), ["the token is logged", "step rv-1"]);
      assert.deepEqual(spansOf(verdicts[1]), ["tests", "holds"]);
      assert.deepEqual(paragraphsOf(verdicts[1]), ["step rv-2"], "a verdict with no reason draws no reason paragraph");

      const ledger = sectionOf(container, "LEDGER");
      assert.equal(ledger?.querySelector("h3.eyebrow > span.sub"), null);
      const findings = itemsOf(ledger);
      assert.equal(findings.length, 2);
      assert.deepEqual(spansOf(findings[0]), ["security-1", "security", "confirmed"]);
      assert.deepEqual(spanClassesOf(findings[0]), ["path", "kind", "state"]);
      assert.deepEqual(paragraphsOf(findings[0]), [
        "the token is logged",
        "internal/auth/log.go:41",
        "the log line prints it",
      ]);
      assert.deepEqual(nestedItemsOf(findings[0]), [], "a finding with no closures draws no nested list");

      assert.deepEqual(spansOf(findings[1]), ["tests-1", "tests", "unverified"], "an unverified finding draws unverified");
      assert.deepEqual(
        paragraphsOf(findings[1]),
        ["<b>untested</b> claim", "no verifier ran"],
        "a finding with no location draws no location paragraph",
      );
      assert.deepEqual(
        nestedItemsOf(findings[1]).map((closures) => closures.map((closure) => closure.textContent)),
        [["round 1 · open", "round 2 · closed · fixed in a2"]],
      );
    },
  ));

test("a released epic draws its release steps with their runs, then the candidate and judge runs", () =>
  withHandover(
    {
      epic: { ...epic, outcome: "released" },
      contract: {
        releaseSteps: [
          { step: "publish tag", target: "github", undo: "delete the tag" },
          { step: "push image", target: "registry", undo: "delete the image tag" },
        ],
      },
      releases: [
        {
          part: "release",
          position: 1n,
          attempt: 1n,
          step: "publish tag",
          start: "a1",
          head: "b1",
          end: "failed",
          commits: [],
          evidence: "<b>denied</b>",
          passed: false,
          reason: "push rejected",
          at: 0n,
        },
        {
          part: "candidate",
          position: 1n,
          attempt: 1n,
          step: "build",
          start: "a1",
          head: "",
          end: "done",
          commits: [],
          evidence: "",
          passed: true,
          reason: "",
          at: 0n,
        },
        {
          part: "judge",
          position: 1n,
          attempt: 1n,
          step: "judge",
          start: "a1",
          head: "",
          end: "done",
          commits: [],
          evidence: "verdict: holds",
          passed: true,
          reason: "",
          at: 0n,
        },
        {
          part: "release",
          position: 1n,
          attempt: 2n,
          step: "publish tag",
          start: "a2",
          head: "b2",
          end: "done",
          commits: ["c1", "c2"],
          evidence: "pushed v1",
          passed: true,
          reason: "pushed",
          at: 0n,
        },
        {
          part: "release",
          position: 2n,
          attempt: 2n,
          step: "push image",
          start: "b2",
          head: "",
          end: "",
          commits: [],
          evidence: "",
          passed: true,
          reason: "",
          at: 0n,
        },
      ],
    },
    (container) => {
      const release = sectionOf(container, "RELEASE");
      assert.equal(release?.querySelector("h3.eyebrow > span.sub")?.textContent, "5", "the sub counts the runs");
      const rows = itemsOf(release);
      assert.deepEqual(
        rows.map((row) => spansOf(row)[0]),
        ["publish tag", "push image", "candidate", "judge"],
        "two step rows, then the candidate and judge rows",
      );

      const publish = rows[0];
      assert.deepEqual(spansOf(publish).slice(0, 3), ["publish tag", "github", "undo delete the tag"]);
      assert.deepEqual(spanClassesOf(publish).slice(0, 2), ["path", "kind"]);
      const publishRuns = nestedItemsOf(publish);
      assert.equal(publishRuns.length, 1, "one nested list of the step's runs");
      assert.deepEqual(
        publishRuns[0].map((run) => spansOf(run)),
        [
          ["attempt 1", "failed", "failed", "⎇"],
          ["attempt 2", "done", "passed", "⎇", "⎇", "⎇"],
        ],
        "the failed attempt comes before the passed one",
      );
      assert.deepEqual(
        publishRuns[0].map((run) => spanClassesOf(run)),
        [
          [null, "end", "state", "commit-mark"],
          [null, "end", "state", "commit-mark", "commit-mark", "commit-mark"],
        ],
      );
      assert.deepEqual(
        publishRuns[0].map((run) => paragraphsOf(run)),
        [["push rejected"], ["pushed"]],
      );
      const [failedRun, passedRun] = publishRuns[0];
      assert.deepEqual(
        [...failedRun.querySelectorAll("pre")].map((pre) => pre.textContent),
        ["<b>denied</b>"],
      );
      assert.equal(failedRun.querySelector("pre")?.getAttribute("class"), "log");
      assert.deepEqual(
        [...passedRun.querySelectorAll("pre")].map((pre) => pre.textContent),
        ["pushed v1"],
      );

      const image = rows[1];
      assert.deepEqual(spansOf(image), ["push image", "registry", "undo delete the image tag"]);
      const imageRuns = nestedItemsOf(image);
      assert.equal(imageRuns.length, 1);
      assert.deepEqual(
        imageRuns[0].map((run) => spansOf(run)),
        [["attempt 2", "passed"]],
        "a run with no end, head, commit, reason or evidence draws only its attempt and state",
      );
      assert.equal(image.querySelectorAll("pre").length, 0);

      const candidate = rows[2];
      assert.deepEqual(spansOf(candidate), ["candidate", "attempt 1", "done", "passed"]);
      assert.deepEqual(nestedItemsOf(candidate), [], "a run row with no nested list");
      assert.equal(candidate.querySelectorAll("p, pre").length, 0, "no reason or evidence when unset");

      const judge = rows[3];
      assert.deepEqual(spansOf(judge), ["judge", "attempt 1", "done", "passed"]);
      assert.deepEqual(
        [...judge.querySelectorAll("pre")].map((pre) => pre.textContent),
        ["verdict: holds"],
      );
    },
  ));

test("a release step with no run draws no nested list, and a run whose step is absent draws as its own row", () =>
  withHandover(
    {
      epic: { ...epic, outcome: "released" },
      contract: { releaseSteps: [{ step: "publish tag", target: "github", undo: "delete the tag" }] },
      releases: [{ part: "release", position: 4n, attempt: 1n, end: "failed", passed: false }],
    },
    (container) => {
      const rows = itemsOf(sectionOf(container, "RELEASE"));
      assert.equal(rows.length, 2);
      assert.deepEqual(nestedItemsOf(rows[0]), []);
      assert.deepEqual(spansOf(rows[1]), ["release", "attempt 1", "failed", "failed"]);
    },
  ));

test("CommitMark: a release run's head and commits draw as ⎇ marks titled with the hash, and no hash shows", () => {
  const head = "0123456789abcdef0123456789abcdef01234567";
  const commits = ["89abcdef0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210fedcba98"];
  return withHandover(
    {
      contract: { releaseSteps: [{ step: "publish tag", target: "github", undo: "delete the tag" }] },
      releases: [
        {
          part: "release",
          position: 1n,
          attempt: 1n,
          step: "publish tag",
          start: "a1",
          head,
          end: "done",
          commits,
          evidence: "",
          passed: true,
          reason: "",
          at: 0n,
        },
      ],
    },
    (container) => {
      const marks = [...container.querySelectorAll("section .commit-mark")];
      assert.deepEqual(
        marks.map((mark) => mark.textContent),
        ["⎇", "⎇", "⎇"],
        "the head and each commit draw a mark",
      );
      assert.deepEqual(marks.map((mark) => mark.getAttribute("title")), [head, ...commits]);
      assert.deepEqual(marks.map((mark) => mark.getAttribute("aria-label")), [head, ...commits]);
      const visible = container.textContent ?? "";
      for (const hash of [head, ...commits]) {
        assert.equal(visible.includes(hash), false, `${hash} is not visible text`);
      }
    },
  );
});

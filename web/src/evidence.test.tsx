import assert from "node:assert/strict";
import { type TestContext, test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { createClients, createTransport } from "./client";
import { EvidenceRuns, type LoadArtifact } from "./evidence";
import { type EvidenceRun, EvidenceRunSchema } from "./gen/orrery/v1/epics_pb";
import { installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

function run(init: MessageInitShape<typeof EvidenceRunSchema>): EvidenceRun {
  return create(EvidenceRunSchema, init);
}

// revision is a full commit id. No run draws it: a run line shows no revision and no commit mark.
const revision = "0123456789abcdef0123456789abcdef01234567";

// redPassed is a red run whose capture exited 1 and whose judge found the conclusion fails: red passes.
const redPassed = run({
  id: "r-red",
  phase: "red",
  source: "captured",
  state: "ended",
  verdict: "fails",
  passed: true,
  exit: 1,
  revision,
});

// screenshot is a green run that kept one image, and the bytes its loader answers with.
const screenshot = run({
  id: "r-shot",
  phase: "green",
  source: "captured",
  state: "ended",
  verdict: "holds",
  passed: true,
  exit: 0,
  revision,
  artifacts: [{ path: "shots/after.png", state: "kept", size: 4n, mediaType: "image/png" }],
});
const pngBytes = new Uint8Array([137, 80, 78, 71]);

// capture is a green run whose capture wrote both logs, each with its own text.
const capture = run({
  id: "r-capture",
  phase: "green",
  source: "captured",
  state: "ended",
  verdict: "holds",
  passed: true,
  exit: 0,
  revision,
  logBytes: 17n,
  stderrBytes: 14n,
});
const stdoutText = "compiling\nlinked\n";
const stderrText = "warning: slow\n";

type Answer = { data: Uint8Array; mediaType: string };

// answering is a loader that answers the calls keyed by run and name, and rejects every other. A stderr log is keyed
// with a stderr suffix. It records each call as its run, name and stderr flag, in order.
function answering(answers: Record<string, Answer>): { load: LoadArtifact; calls: [string, string, boolean][] } {
  const calls: [string, string, boolean][] = [];
  const load: LoadArtifact = (runId, name, stderr) => {
    calls.push([runId, name, stderr]);
    const key = stderr ? `${runId}/${name}/stderr` : `${runId}/${name}`;
    const answer = answers[key];
    return answer === undefined ? Promise.reject(new Error(`no artifact ${key}`)) : Promise.resolve(answer);
  };
  return { load, calls };
}

// settle is one turn of the event loop inside act, so the promises a load left and the renders they cause are applied.
function settle(): Promise<void> {
  return act(async () => {
    await turn();
  });
}

// waitFor steps settle until check holds. The test's own signal ends the wait when the test ends.
function waitFor(t: TestContext, check: () => boolean, what: string): Promise<void> {
  return until(t.signal, check, what, { step: settle });
}

// withRuns renders the runs with the loader, and runs check on the container, with the runs' logs open when initiallyOpen.
async function withRuns(
  t: TestContext,
  runs: EvidenceRun[],
  load: LoadArtifact,
  check: (container: HTMLElement) => void | Promise<void>,
  initiallyOpen = false,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<EvidenceRuns runs={runs} loadArtifact={load} initiallyOpen={initiallyOpen} />, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// press clicks one run's log toggle, the index-th toggle on the page, inside act, so the renders it causes and the
// loads its effects start are applied.
function press(container: HTMLElement, index: number): void {
  const toggle = container.querySelectorAll<HTMLButtonElement>(".run-line .link")[index];
  assert.ok(toggle, `the page has log toggle ${index}`);
  act(() => {
    toggle.click();
  });
}

function textsOf(root: ParentNode, selector: string): string[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent ?? "");
}

function runLinesOf(container: HTMLElement): Element[] {
  return [...container.querySelectorAll(".run-line")];
}

function logsOf(container: HTMLElement): Element[] {
  return [...container.querySelectorAll(".log")];
}

// logBodyOf is a log's text without its label, which is the first child of the log.
function logBodyOf(log: Element): string {
  const label = log.querySelector(".lbl")?.textContent ?? "";
  return (log.textContent ?? "").slice(label.length);
}

function bodiesOf(container: HTMLElement): string[] {
  return logsOf(container).map(logBodyOf);
}

test("EpicProof: a red run that passed reads its sentence, a fails chip, and its meta with the exit", (t) =>
  withRuns(t, [redPassed], answering({}).load, (container) => {
    const [line] = runLinesOf(container);
    assert.equal(container.querySelectorAll(".run").length, 1);
    assert.equal(line?.querySelector(".sentence")?.textContent, "Red run: the test fails before any change, as it should.");
    assert.equal(line?.querySelector(".chip")?.getAttribute("class"), "chip bad");
    assert.equal(line?.querySelector(".chip")?.textContent, "✗fails");
    assert.equal(line?.querySelector(".meta")?.textContent, "captured · exit 1");
  }));

test("EpicProof: a red run that did not pass reads that the test passes before any change, with a holds chip", (t) => {
  const passedAtBase = run({
    id: "r-met",
    phase: "red",
    source: "captured",
    state: "ended",
    verdict: "holds",
    passed: false,
    exit: 0,
  });
  return withRuns(t, [passedAtBase], answering({}).load, (container) => {
    const [line] = runLinesOf(container);
    assert.equal(line?.querySelector(".sentence")?.textContent, "Red run: the test passes before any change. It should fail.");
    assert.equal(line?.querySelector(".chip")?.getAttribute("class"), "chip ok");
    assert.equal(line?.querySelector(".chip")?.textContent, "✓holds");
  });
});

test("EpicProof: a green run reads whether the test passes on the change, with its verdict chip", (t) => {
  const passes = run({ id: "r-g1", phase: "green", source: "captured", state: "ended", verdict: "holds", passed: true, exit: 0 });
  const fails = run({ id: "r-g2", phase: "green", source: "captured", state: "ended", verdict: "fails", passed: false, exit: 1 });
  return withRuns(t, [passes, fails], answering({}).load, (container) => {
    const lines = runLinesOf(container);
    assert.deepEqual(
      lines.map((line) => line.querySelector(".sentence")?.textContent),
      ["Green run: the test passes on the change.", "Green run: the test still fails on the change."],
    );
    assert.deepEqual(
      lines.map((line) => line.querySelector(".chip")?.getAttribute("class")),
      ["chip ok", "chip bad"],
    );
  });
});

test("EpicProof: a supplied run's meta reads supplied by the agent, then its exit", (t) => {
  const supplied = run({
    id: "r-supplied",
    phase: "green",
    source: "supplied",
    state: "ended",
    verdict: "holds",
    passed: true,
    exit: 0,
  });
  return withRuns(t, [supplied], answering({}).load, (container) => {
    assert.equal(runLinesOf(container)[0]?.querySelector(".meta")?.textContent, "supplied by the agent · exit 0");
  });
});

test("EpicProof: a run that is capturing reads its capturing sentence, a live chip, and no exit", (t) => {
  const capturing = run({ id: "r-capturing", phase: "red", source: "captured", state: "capturing" });
  return withRuns(t, [capturing], answering({}).load, (container) => {
    const [line] = runLinesOf(container);
    assert.equal(line?.querySelector(".sentence")?.textContent, "Red run: capturing the output…");
    assert.equal(line?.querySelector(".chip")?.getAttribute("class"), "chip live");
    assert.equal(line?.querySelector(".chip")?.textContent, "capturing");
    assert.equal(line?.querySelector(".meta")?.textContent, "captured");
  });
});

test("EpicProof: a run that is judging reads that a judge is reading the output, with a live judging chip", (t) => {
  const judging = run({ id: "r-judging", phase: "green", source: "captured", state: "judging" });
  return withRuns(t, [judging], answering({}).load, (container) => {
    const [line] = runLinesOf(container);
    assert.equal(line?.querySelector(".sentence")?.textContent, "Green run: a judge is reading the output…");
    assert.equal(line?.querySelector(".chip")?.textContent, "judging");
    assert.equal(line?.querySelector(".meta")?.textContent, "captured");
  });
});

test("EpicProof: a run whose output was not captured reads why it was not", (t) => {
  const uncaptured = run({
    id: "r-uncaptured",
    phase: "green",
    source: "captured",
    state: "ended",
    exit: 137,
    notCaptured: "the capture was killed at its limit",
  });
  return withRuns(t, [uncaptured], answering({}).load, (container) => {
    assert.equal(
      runLinesOf(container)[0]?.querySelector(".sentence")?.textContent,
      "Green run: the output was not captured: the capture was killed at its limit.",
    );
  });
});

test("EpicProof: a run with no verdict reads no verdict in a neutral chip, with the judge's reason after it", (t) => {
  const unjudged = run({
    id: "r-green",
    phase: "green",
    source: "captured",
    state: "ended",
    verdict: "",
    judgeReason: "the judge step ended gate_failed",
    passed: false,
    exit: 0,
  });
  const bare = run({ id: "r-bare", phase: "green", source: "captured", state: "ended", verdict: "", exit: 0 });
  return withRuns(t, [unjudged, bare], answering({}).load, (container) => {
    const chips = runLinesOf(container).map((line) => line.querySelector(".chip"));
    assert.deepEqual(
      chips.map((chip) => chip?.getAttribute("class")),
      ["chip", "chip"],
    );
    assert.deepEqual(
      chips.map((chip) => chip?.textContent),
      ["no verdict · the judge step ended gate_failed", "no verdict"],
    );
  });
});

test("EpicProof: a run has one log toggle that reads log, and hide the log while it is open, even with a stderr log", (t) =>
  withRuns(t, [capture], answering({}).load, (container) => {
    const toggle = () => container.querySelector<HTMLButtonElement>(".run-line .link");
    assert.equal(container.querySelectorAll(".run-line .link").length, 1);
    assert.equal(toggle()?.textContent, "log");
    assert.equal(toggle()?.getAttribute("aria-expanded"), "false");
    press(container, 0);
    assert.equal(toggle()?.textContent, "hide the log");
    assert.equal(toggle()?.getAttribute("aria-expanded"), "true");
    press(container, 0);
    assert.equal(toggle()?.textContent, "log");
    assert.equal(toggle()?.getAttribute("aria-expanded"), "false");
  }));

test("EpicProof: an open log shows stdout, then stderr when the run has stderr bytes", (t) => {
  const { load, calls } = answering({
    "r-capture/": { data: new TextEncoder().encode(stdoutText), mediaType: "text/plain" },
    "r-capture//stderr": { data: new TextEncoder().encode(stderrText), mediaType: "text/plain" },
  });
  return withRuns(t, [capture], load, async (container) => {
    press(container, 0);
    await waitFor(t, () => bodiesOf(container).length === 2 && !bodiesOf(container).includes("loading the log…"), "both logs");
    assert.deepEqual(logsOf(container).map((log) => log.querySelector(".lbl")?.textContent), ["stdout", "stderr"]);
    assert.deepEqual(bodiesOf(container), [stdoutText, stderrText]);
    assert.deepEqual(calls, [
      ["r-capture", "", false],
      ["r-capture", "", true],
    ]);
  });
});

test("EpicProof: an open log of a run with no stderr bytes shows stdout alone", (t) => {
  const { load, calls } = answering({ "r-red/": { data: new TextEncoder().encode("step one\n"), mediaType: "text/plain" } });
  return withRuns(t, [redPassed], load, async (container) => {
    press(container, 0);
    await waitFor(t, () => bodiesOf(container).includes("step one\n"), "the stdout log");
    assert.deepEqual(logsOf(container).map((log) => log.querySelector(".lbl")?.textContent), ["stdout"]);
    assert.deepEqual(calls, [["r-red", "", false]]);
  });
});

test("EpicProof: an open log reads loading the log… until its bytes arrive", (t) => {
  let land: (answer: Answer) => void = () => {};
  const load: LoadArtifact = () =>
    new Promise<Answer>((resolve) => {
      land = resolve;
    });
  return withRuns(t, [redPassed], load, async (container) => {
    press(container, 0);
    await waitFor(t, () => bodiesOf(container).join() === "loading the log…", "the loading line");
    land({ data: new TextEncoder().encode("step one\n"), mediaType: "text/plain" });
    await waitFor(t, () => bodiesOf(container).join() === "step one\n", "the arrived log");
  });
});

test("EpicProof: a log that does not load reads The log is not available.", (t) =>
  withRuns(t, [redPassed], answering({}).load, async (container) => {
    press(container, 0);
    await waitFor(t, () => bodiesOf(container).join() === "The log is not available.", "the unavailable line");
    assert.equal(logsOf(container).length, 1);
  }));

test("EpicProof: markup in a log renders as text, never as elements", (t) => {
  const markup = '<img src="x" onerror="alert(1)"><b>bold</b>';
  const { load } = answering({ "r-red/": { data: new TextEncoder().encode(markup), mediaType: "text/plain" } });
  return withRuns(t, [redPassed], load, async (container) => {
    press(container, 0);
    await waitFor(t, () => bodiesOf(container).join() === markup, "the log text");
    assert.equal(container.querySelectorAll("img, b").length, 0);
  });
});

test("EpicProof: artifacts fold with the log, so none is listed or loaded while the log is shut", (t) => {
  const { load, calls } = answering({ "r-shot/": { data: new TextEncoder().encode("ok\n"), mediaType: "text/plain" } });
  return withRuns(t, [screenshot], load, (container) => {
    assert.equal(container.querySelectorAll(".artifact").length, 0);
    assert.equal(calls.length, 0);
  });
});

test("EpicProof: a kept image artifact reads its path, kept and size, and draws the image from an object URL", (t) => {
  const { load, calls } = answering({
    "r-shot/": { data: new TextEncoder().encode("ok\n"), mediaType: "text/plain" },
    "r-shot/shots/after.png": { data: pngBytes, mediaType: "image/png" },
  });
  return withRuns(t, [screenshot], load, async (container) => {
    press(container, 0);
    await waitFor(t, () => container.querySelector("img") !== null, "the image");
    assert.deepEqual(calls, [
      ["r-shot", "", false],
      ["r-shot", "shots/after.png", false],
    ]);
    assert.equal(textsOf(container, ".artifact").join(), "shots/after.png · kept · 4 bytes");
    assert.equal(container.querySelectorAll("img").length, 1);
    assert.equal(container.querySelector("img")?.getAttribute("src")?.startsWith("blob:"), true);
    assert.equal(container.querySelector("img")?.getAttribute("alt") ?? null, "shots/after.png");
  });
});

test("EpicProof: closing the log unmounts a kept image and revokes its object URL", async (t) => {
  const revoked: string[] = [];
  const revoke = URL.revokeObjectURL;
  URL.revokeObjectURL = (url: string) => {
    revoked.push(url);
  };
  try {
    const { load } = answering({
      "r-shot/": { data: new TextEncoder().encode("ok\n"), mediaType: "text/plain" },
      "r-shot/shots/after.png": { data: pngBytes, mediaType: "image/png" },
    });
    await withRuns(t, [screenshot], load, async (container) => {
      press(container, 0);
      await waitFor(t, () => container.querySelector("img") !== null, "the image");
      const src = container.querySelector("img")?.getAttribute("src") ?? null;
      assert.equal(revoked.length, 0);
      press(container, 0);
      assert.equal(container.querySelectorAll("img").length, 0);
      assert.deepEqual(revoked, [src]);
    });
  } finally {
    URL.revokeObjectURL = revoke;
  }
});

test("EpicProof: an image that does not load reads image unavailable, and draws no image", (t) => {
  const { load } = answering({ "r-shot/": { data: new TextEncoder().encode("ok\n"), mediaType: "text/plain" } });
  return withRuns(t, [screenshot], load, async (container) => {
    press(container, 0);
    await waitFor(t, () => container.querySelector(".artifact .unavailable") !== null, "the image line");
    assert.equal(textsOf(container, ".artifact .unavailable").join(), "image unavailable");
    assert.equal(container.querySelectorAll("img").length, 0);
  });
});

test("EpicProof: a missing and an over-cap artifact read as missing and too large to keep, and draw no image", (t) => {
  const gaps = run({
    id: "r-gaps",
    phase: "red",
    source: "captured",
    state: "ended",
    verdict: "fails",
    passed: true,
    exit: 1,
    artifacts: [
      { path: "gone.png", state: "missing", size: 0n, mediaType: "image/png" },
      { path: "huge.png", state: "over_cap", size: 11534336n, mediaType: "image/png" },
    ],
  });
  const { load, calls } = answering({ "r-gaps/": { data: new TextEncoder().encode("ok\n"), mediaType: "text/plain" } });
  return withRuns(t, [gaps], load, async (container) => {
    press(container, 0);
    await waitFor(t, () => container.querySelectorAll(".artifact").length === 2, "both artifact lines");
    assert.deepEqual(textsOf(container, ".artifact"), ["gone.png · missing · 0 bytes", "huge.png · too large to keep · 11534336 bytes"]);
    assert.equal(container.querySelectorAll("img").length, 0);
    assert.deepEqual(
      calls.filter(([, name]) => name !== ""),
      [],
    );
  });
});

test("EpicProof: a kept artifact that is not an image reads its line and is not loaded", (t) => {
  const notes = run({
    id: "r-notes",
    phase: "red",
    source: "captured",
    state: "ended",
    verdict: "fails",
    passed: true,
    exit: 1,
    artifacts: [{ path: "notes/run.txt", state: "kept", size: 12n, mediaType: "text/plain" }],
  });
  const { load, calls } = answering({ "r-notes/": { data: new TextEncoder().encode("ok\n"), mediaType: "text/plain" } });
  return withRuns(t, [notes], load, async (container) => {
    press(container, 0);
    await waitFor(t, () => container.querySelectorAll(".artifact").length === 1, "the artifact line");
    assert.equal(textsOf(container, ".artifact").join(), "notes/run.txt · kept · 12 bytes");
    assert.equal(container.querySelectorAll("img").length, 0);
    assert.deepEqual(
      calls.filter(([, name]) => name !== ""),
      [],
    );
  });
});

test("EpicProof: a run draws no revision and no commit mark", (t) =>
  withRuns(t, [redPassed], answering({}).load, (container) => {
    assert.equal(container.querySelectorAll(".commit-mark").length, 0);
    const visible = container.textContent ?? "";
    assert.equal(visible.includes(revision), false, "the full id is not visible text");
    assert.equal(visible.includes(revision.slice(0, 12)), false, "no leading part of the id is visible text");
  }));

test("EpicProof: initiallyOpen draws each run's log open from the start", (t) => {
  const { load } = answering({ "r-red/": { data: new TextEncoder().encode("step one\n"), mediaType: "text/plain" } });
  return withRuns(
    t,
    [redPassed],
    load,
    async (container) => {
      await waitFor(t, () => bodiesOf(container).join() === "step one\n", "the open log");
      assert.equal(runLinesOf(container).length, 1);
      assert.equal(container.querySelector(".run-line .link")?.textContent, "hide the log");
    },
    true,
  );
});

test("the evidence client posts Evidence.Artifact with the run and name to the base URL", async () => {
  const seen: { url?: string; method?: string; body?: string; header?: string | null } = {};
  const fakeFetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    seen.url = request.url;
    seen.method = request.method;
    seen.header = request.headers.get("X-Orrery-Client");
    seen.body = await request.text();
    return new Response("{}", { headers: { "content-type": "application/json" } });
  };
  const transport = createTransport({ baseUrl: "https://orrery.test", fetch: fakeFetch });
  await createClients(transport).evidence.artifact({ run: "r-red", name: "" });
  assert.equal(seen.url, "https://orrery.test/orrery.v1.Evidence/Artifact");
  assert.equal(seen.method, "POST");
  assert.equal(JSON.parse(seen.body ?? "{}").run, "r-red");
  assert.ok(seen.header?.trim(), `X-Orrery-Client header was ${JSON.stringify(seen.header)}`);
});

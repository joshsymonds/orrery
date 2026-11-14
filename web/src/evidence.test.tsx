import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";

import { createClients, createTransport } from "./client";
import { EvidenceRuns, type LoadArtifact } from "./evidence";
import { type EvidenceRun, EvidenceRunSchema } from "./gen/orrery/v1/epics_pb";
import { installTestDom, removeTestDom } from "./testdom";

function run(init: MessageInitShape<typeof EvidenceRunSchema>): EvidenceRun {
  return create(EvidenceRunSchema, init);
}

// revision is a full commit id, of which a run shows the first twelve characters.
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

type Answer = { data: Uint8Array; mediaType: string };

// answering is a loader that answers the calls keyed by run and name, and rejects every other. It records each call as
// its run and name, in order.
function answering(answers: Record<string, Answer>): { load: LoadArtifact; calls: [string, string][] } {
  const calls: [string, string][] = [];
  const load: LoadArtifact = (runId, name) => {
    calls.push([runId, name]);
    const answer = answers[`${runId}/${name}`];
    return answer === undefined ? Promise.reject(new Error(`no artifact ${runId}/${name}`)) : Promise.resolve(answer);
  };
  return { load, calls };
}

// settled polls until holds is true, a turn of the event loop at a time, for up to a second. Preact runs the effects of
// a render after a paint timer, so a load that an effect starts lands on a later turn than the click that caused it.
async function settled(holds: () => boolean): Promise<void> {
  for (let turn = 0; turn < 100 && !holds(); turn += 1) {
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return [...root.querySelectorAll(selector)].map((element) => element.textContent);
}

async function withRuns(
  runs: EvidenceRun[],
  load: LoadArtifact,
  check: (container: HTMLElement) => void | Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<EvidenceRuns runs={runs} loadArtifact={load} />, container);
    await check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

test("a red run that passed reads its phase, source, state, verdict, result, exit and short revision", () =>
  withRuns([redPassed], answering({}).load, (container) => {
    assert.equal(container.querySelectorAll(".run").length, 1);
    assert.equal(textOf(container, ".run .phase"), "red");
    assert.equal(textOf(container, ".run .source"), "captured");
    assert.equal(textOf(container, ".run .state"), "ended");
    assert.equal(textOf(container, ".run .verdict"), "fails");
    assert.equal(textOf(container, ".run .pass"), "passed");
    assert.equal(textOf(container, ".run .exit"), "exit 1");
    assert.equal(textOf(container, ".run .revision"), "0123456789ab");
  }));

test("a green run with no verdict reads no verdict with the judge's reason, and not passed", () => {
  const unjudged = run({
    id: "r-green",
    phase: "green",
    source: "captured",
    state: "ended",
    verdict: "",
    judgeReason: "the judge step ended gate_failed",
    passed: false,
    exit: 0,
    revision,
  });
  return withRuns([unjudged], answering({}).load, (container) => {
    assert.equal(textOf(container, ".run .verdict"), "no verdict · the judge step ended gate_failed");
    assert.equal(textOf(container, ".run .pass"), "not passed");
    assert.equal(textOf(container, ".run .exit"), "exit 0");
  });
});

test("a supplied run reads agent-supplied as its source", () => {
  const supplied = run({
    id: "r-supplied",
    phase: "red",
    source: "supplied",
    state: "ended",
    verdict: "holds",
    passed: false,
    exit: 1,
    revision,
  });
  return withRuns([supplied], answering({}).load, (container) => {
    assert.equal(textOf(container, ".run .source"), "agent-supplied");
    assert.equal(textOf(container, ".run .verdict"), "holds");
    assert.equal(textOf(container, ".run .pass"), "not passed");
  });
});

test("a run that has not ended reads running, and shows no exit", () => {
  const capturing = run({ id: "r-capturing", phase: "red", source: "captured", state: "capturing", revision });
  const judging = run({ id: "r-judging", phase: "green", source: "captured", state: "judging", revision });
  return withRuns([capturing, judging], answering({}).load, (container) => {
    assert.deepEqual(textsOf(container, ".run .state"), ["running", "running"]);
    assert.equal(container.querySelectorAll(".run .exit").length, 0);
    assert.deepEqual(textsOf(container, ".run .verdict"), ["no verdict", "no verdict"]);
  });
});

test("the log toggle loads the run's log with an empty name on open, shows its text, and closes", async () => {
  const text = "step one\nstep two\n";
  const { load, calls } = answering({ "r-red/": { data: new TextEncoder().encode(text), mediaType: "text/plain" } });
  await withRuns([redPassed], load, async (container) => {
    assert.equal(container.querySelector(".log-toggle")?.getAttribute("aria-expanded") ?? null, "false");
    container.querySelector<HTMLButtonElement>(".log-toggle")?.click();
    await settled(() => container.querySelector(".log") !== null);
    assert.deepEqual(calls, [["r-red", ""]]);
    assert.equal(textOf(container, ".log"), text);
    assert.equal(container.querySelector(".log-toggle")?.getAttribute("aria-expanded") ?? null, "true");
    container.querySelector<HTMLButtonElement>(".log-toggle")?.click();
    await settled(() => container.querySelector(".log") === null);
    assert.equal(container.querySelectorAll(".log").length, 0);
    assert.equal(calls.length, 1, "closing loads nothing");
  });
});

test("a log that does not load says so", async () => {
  await withRuns([redPassed], answering({}).load, async (container) => {
    container.querySelector<HTMLButtonElement>(".log-toggle")?.click();
    await settled(() => container.querySelector(".unavailable") !== null);
    assert.equal(textOf(container, ".unavailable"), "log unavailable");
    assert.equal(container.querySelectorAll(".log").length, 0);
  });
});

test("markup in a log renders as text, never as elements", async () => {
  const markup = '<img src="x" onerror="alert(1)"><b>bold</b>';
  const { load } = answering({ "r-red/": { data: new TextEncoder().encode(markup), mediaType: "text/plain" } });
  await withRuns([redPassed], load, async (container) => {
    container.querySelector<HTMLButtonElement>(".log-toggle")?.click();
    await settled(() => container.querySelector(".log") !== null);
    assert.equal(textOf(container, ".log"), markup);
    assert.equal(container.querySelectorAll(".log *").length, 0);
    assert.equal(container.querySelectorAll("img, b").length, 0);
  });
});

test("an image artifact is loaded by its path and drawn from an object URL", async () => {
  const { load, calls } = answering({ "r-shot/shots/after.png": { data: pngBytes, mediaType: "image/png" } });
  await withRuns([screenshot], load, async (container) => {
    await settled(() => container.querySelector("img") !== null);
    assert.deepEqual(calls, [["r-shot", "shots/after.png"]]);
    assert.equal(container.querySelectorAll("img").length, 1);
    assert.equal(container.querySelector("img")?.getAttribute("src")?.startsWith("blob:"), true);
    assert.equal(container.querySelector("img")?.getAttribute("alt") ?? null, "shots/after.png");
    assert.equal(textOf(container, ".artifact .path"), "shots/after.png");
    assert.equal(textOf(container, ".artifact .artifact-state"), "kept");
    assert.equal(textOf(container, ".artifact .size"), "4 bytes");
  });
});

test("an image's object URL is revoked when its row unmounts", async () => {
  const revoked: string[] = [];
  const revoke = URL.revokeObjectURL;
  URL.revokeObjectURL = (url: string) => {
    revoked.push(url);
  };
  try {
    const { load } = answering({ "r-shot/shots/after.png": { data: pngBytes, mediaType: "image/png" } });
    await withRuns([screenshot], load, async (container) => {
      await settled(() => container.querySelector("img") !== null);
      const src = container.querySelector("img")?.getAttribute("src") ?? null;
      assert.equal(revoked.length, 0);
      render(null, container);
      assert.deepEqual(revoked, [src]);
    });
  } finally {
    URL.revokeObjectURL = revoke;
  }
});

test("an image that does not load says so", async () => {
  await withRuns([screenshot], answering({}).load, async (container) => {
    await settled(() => container.querySelector(".unavailable") !== null);
    assert.equal(textOf(container, ".unavailable"), "image unavailable");
    assert.equal(container.querySelectorAll("img").length, 0);
  });
});

test("a missing and an over-cap artifact say so, and draw no image", async () => {
  const gaps = run({
    id: "r-gaps",
    phase: "red",
    source: "captured",
    state: "ended",
    verdict: "fails",
    passed: true,
    exit: 1,
    revision,
    artifacts: [
      { path: "gone.png", state: "missing", size: 0n, mediaType: "image/png" },
      { path: "huge.png", state: "over_cap", size: 11534336n, mediaType: "image/png" },
    ],
  });
  const { load, calls } = answering({});
  await withRuns([gaps], load, (container) => {
    assert.deepEqual(textsOf(container, ".artifact .path"), ["gone.png", "huge.png"]);
    assert.deepEqual(textsOf(container, ".artifact .artifact-state"), ["missing", "over cap"]);
    assert.deepEqual(textsOf(container, ".artifact .size"), ["0 bytes", "11534336 bytes"]);
    assert.equal(container.querySelectorAll("img").length, 0);
    assert.equal(calls.length, 0);
  });
});

test("a kept artifact that is not an image is listed and not loaded", async () => {
  const notes = run({
    id: "r-notes",
    phase: "red",
    source: "captured",
    state: "ended",
    verdict: "fails",
    passed: true,
    exit: 1,
    revision,
    artifacts: [{ path: "notes/run.txt", state: "kept", size: 12n, mediaType: "text/plain" }],
  });
  const { load, calls } = answering({});
  await withRuns([notes], load, (container) => {
    assert.deepEqual(textsOf(container, ".artifact .artifact-state"), ["kept"]);
    assert.equal(container.querySelectorAll("img").length, 0);
    assert.equal(calls.length, 0);
  });
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

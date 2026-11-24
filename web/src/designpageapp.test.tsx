import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, fromJson, toJson } from "@bufbuild/protobuf";
import { render } from "preact";
import { act } from "preact/test-utils";

import { App } from "./app";
import { createClients, createTransport } from "./client";
import {
  type Design,
  type DesignQuestion,
  DesignLogLineSchema,
  DesignQuestionSchema,
  DesignSchema,
  DesignStepSchema,
  DesignsAnswerRequestSchema,
  type DesignsAnswerRequest,
  DesignsGetRequestSchema,
  DesignsGetResponseSchema,
  type DesignsGetResponse,
  RecordMakerSchema,
} from "./gen/orrery/v1/designs_pb";
import type { Row } from "./rows";
import type { SeenClock } from "./seen";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Each test's name begins DesignPage: App. App is mounted at the desk's width, with a fake transport that answers the
// Designs calls from its script and holds each conversation stream open. The checks read the DOM only. No clock runs:
// the seen clock's timers never fire, and each wait steps by one turn of the event loop.

const now = 1000;
const desk = 1600;
const session = "sess-tide";
const getPath = "/orrery.v1.Designs/Get";
const answerPath = "/orrery.v1.Designs/Answer";

// tideRow is the working session sess-tide of /work/tide, which the design works.
const tideRow: Row = {
  tool: "",
  id: session,
  kind: "pane",
  key: session,
  label: "tide work",
  archiveId: session,
  project: "/work/tide",
  host: "",
  state: "working",
  summary: "",
  question: "",
  questionHash: "",
  changed: 300,
  seen: 300,
  unseen: false,
  archived: false,
  putOff: false,
  archiveKind: "session",
};

// stillClock is a seen clock whose timers never fire, so an unseen row under the cursor is never marked seen.
const stillClock: SeenClock = {
  now: () => now * 1000,
  setTimeout: () => 0,
  clearTimeout: () => {},
};

// tideOf is the invented design the session works, in the state given. It has no contract, so its tab stands alone.
function tideOf(state: string): Design {
  return create(DesignSchema, { id: "tide", title: "Tide alerts", conversation: session, created: 10n, state });
}

// makerOf is who made a record of the kind given.
function makerOf(kind: string) {
  return create(RecordMakerSchema, { kind });
}

// discovery is the design's discovery pass, which returned READY and counted.
const discovery = create(DesignStepSchema, {
  step: "s1",
  kind: "discovery",
  subject: "the drafts",
  status: "done",
  verdict: "READY",
  counted: true,
  created: 1n,
});

// stagedRecord is the design's record with a discovery pass and a log of two lines, the conversation's and the owner's.
function stagedRecord(): DesignsGetResponse {
  return create(DesignsGetResponseSchema, {
    design: tideOf("Being researched"),
    stage: { steps: [discovery] },
    log: [
      create(DesignLogLineSchema, {
        seq: 1n,
        time: 1000n,
        maker: makerOf("conversation"),
        what: "asked",
        ids: [],
        words: "",
      }),
      create(DesignLogLineSchema, {
        seq: 2n,
        time: 2000n,
        maker: makerOf("owner"),
        what: "said",
        ids: [],
        words: "Start with the drafts.",
      }),
    ],
  });
}

// questionOf is the design's question Q1, open, or settled with the recommendation accepted by the owner.
function questionOf(settled: boolean): DesignQuestion {
  return create(DesignQuestionSchema, {
    id: 1n,
    text: "Bump the embedded tz database?",
    options: ["Keep the pin", "Allow the bump"],
    recommendation: "Keep the pin",
    asked: 3n,
    ...(settled ? { answer: "Keep the pin", answered: 4n, accepted: true, maker: makerOf("owner") } : {}),
  });
}

// questionsRecord is the design's record with the question given and no other stage part.
function questionsRecord(settled: boolean): DesignsGetResponse {
  return create(DesignsGetResponseSchema, {
    design: tideOf("Asking"),
    stage: { questions: [questionOf(settled)] },
  });
}

// Refusal is a call the fake server fails: its HTTP status, its Connect code and the message it gives.
interface Refusal {
  status: number;
  code: string;
  message: string;
}

// Reply is what the fake server answers a Designs.Get with: the response, or the refusal the call fails with.
type Reply = { response: DesignsGetResponse } | { refusal: Refusal };

// Script is what the fake server answers the Designs calls with. get gives the record of the design asked for. answer
// gives a refusal to fail the Designs.Answer with, or null to let it land.
interface Script {
  get: (id: string) => Reply;
  answer?: (request: DesignsAnswerRequest) => Refusal | null;
}

// Server is the fake the App calls through. It records each Designs.Get's design id and each Designs.Answer's request, in
// the order the calls came.
interface Server {
  fetch: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
  gets: string[];
  answers: DesignsAnswerRequest[];
}

// json is a response of the value given, as JSON.
function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json" } });
}

// refused is the response a failed call carries: its status, and its code and message as JSON.
function refused(refusal: Refusal): Response {
  return json({ code: refusal.code, message: refusal.message }, refusal.status);
}

// fakeServer answers the Designs calls as the script says, holds each conversation stream open with no message, and
// answers every other call with an empty message.
function fakeServer(script: Script): Server {
  const gets: string[] = [];
  const answers: DesignsAnswerRequest[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const path = new URL(request.url).pathname;
    if (path.endsWith("/WatchConversation")) {
      return new Response(new ReadableStream<Uint8Array>({ start() {} }), {
        status: 200,
        headers: { "content-type": "application/connect+json" },
      });
    }
    if (path === getPath) {
      const { id } = fromJson(DesignsGetRequestSchema, JSON.parse(await request.text()));
      gets.push(id);
      const reply = script.get(id);
      return "refusal" in reply ? refused(reply.refusal) : json(toJson(DesignsGetResponseSchema, reply.response));
    }
    if (path === answerPath) {
      const answer = fromJson(DesignsAnswerRequestSchema, JSON.parse(await request.text()));
      answers.push(answer);
      const refusal = script.answer?.(answer) ?? null;
      return refusal === null ? json({}) : refused(refusal);
    }
    return json({});
  };
  return { fetch, gets, answers };
}

// Rig is what a check reaches: App's container, the calls the fake server took, settle, which waits until check holds
// while each turn lets App's calls and renders run, and redraw, which draws App again over the designs given.
interface Rig {
  container: HTMLElement;
  server: Server;
  settle: (check: () => boolean, what: string) => Promise<void>;
  redraw: (designs: Design[]) => Promise<void>;
}

// withApp mounts App at the desk's width over the session's row and the design tide, with a fake server of script behind
// its clients, runs check on it, and unmounts App however check ends.
async function withApp(t: TestContext, script: Script, check: (rig: Rig) => Promise<void>): Promise<void> {
  const window = installTestDom();
  window.innerWidth = desk;
  const container = document.createElement("div");
  document.body.append(container);
  const server = fakeServer(script);
  const clients = createClients(createTransport({ baseUrl: "https://orrery.test", fetch: server.fetch }));
  const draw = (designs: Design[]) =>
    act(() => {
      render(
        <App rows={[tideRow]} now={now} clients={clients} contracts={[]} designs={designs} seenClock={stillClock} />,
        container,
      );
    });
  const teardown = teardownOnce(t.signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  try {
    await draw([tideOf("Being researched")]);
    await check({
      container,
      server,
      settle: (done, what) => until(t.signal, done, what, { step: () => act(turn) }),
      redraw: draw,
    });
  } finally {
    await teardown();
  }
}

// press clicks an element and lets Preact draw what the click changed.
async function press(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click"));
  });
}

// required is the element that matches selector under root, which must be drawn.
function required(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// holdTide presses the design's tab, which holds the design's page at the desk.
function holdTide(container: HTMLElement): Promise<void> {
  return press(required(container, '.list .design-tab[data-key="design:tide"]'));
}

// buttonNamed is the button under root whose text is label, which must be drawn.
function buttonNamed(root: ParentNode, label: string): HTMLElement {
  const found = [...root.querySelectorAll<HTMLElement>("button")].find((button) => button.textContent === label);
  assert.ok(found !== undefined, `no button reads ${label}`);
  return found;
}

test("DesignPage: App holding a design calls Designs.Get for it and draws its strip, steelman and log", (t) =>
  withApp(t, { get: () => ({ response: stagedRecord() }) }, async ({ container, server, settle }) => {
    await holdTide(container);
    await settle(() => container.querySelector("section.design-log") !== null, "the held design's log is drawn");
    assert.deepEqual(server.gets, ["tide"], "holding the design does not read it once, with Designs.Get");
    assert.equal(
      required(container, 'nav[aria-label="where this design is"] .stage.now').getAttribute("data-stage"),
      "Steelman",
      "the strip does not show the stage the record reaches",
    );
    assert.ok(
      container.querySelector('section.design-steelman[aria-label="the steelman"] li.ss-pass[data-step="s1"]') !== null,
      "the steelman does not draw the discovery pass",
    );
    assert.deepEqual(
      [...container.querySelectorAll("section.design-log li.design-log-line .design-log-what")].map(
        (what) => what.textContent,
      ),
      ["asked", "said"],
      "the log does not draw its lines, oldest first",
    );
  }));

test("DesignPage: App Accept on an open question answers it once with accept, and the redrawn page shows it settled", (t) => {
  let settled = false;
  const script: Script = {
    get: () => ({ response: questionsRecord(settled) }),
    answer: () => {
      settled = true;
      return null;
    },
  };
  return withApp(t, script, async ({ container, server, settle }) => {
    await holdTide(container);
    await settle(() => container.querySelector('[aria-label="question Q1"].qthread') !== null, "the open question is drawn");
    await press(buttonNamed(required(container, '[aria-label="question Q1"]'), "Accept"));
    await settle(() => container.querySelector('[aria-label="question Q1"].settled') !== null, "the question is drawn settled");
    assert.deepEqual(
      server.answers.map((answer) => ({ design: answer.design, question: answer.question, accept: answer.accept })),
      [{ design: "tide", question: 1n, accept: true }],
      "Accept does not answer Q1 once, with accept",
    );
    assert.deepEqual(server.gets, ["tide", "tide"], "the design is not read again once the answer lands");
    assert.equal(
      required(container, '[aria-label="question Q1"] b').textContent,
      "Keep the pin",
      "the settled question does not show its answer",
    );
  });
});

test("DesignPage: App a failed Designs.Get draws the error line where the design's stage is, and no stage section", (t) =>
  withApp(
    t,
    { get: () => ({ refusal: { status: 503, code: "unavailable", message: "orreryd is not answering" } }) },
    async ({ container, settle }) => {
      await holdTide(container);
      await settle(() => container.querySelector(".design-page .inote.error") !== null, "the error line is drawn");
      assert.equal(
        required(container, ".design-page .inote.error").textContent,
        "The server could not read this design.",
        "the error line does not say the design could not be read",
      );
      assertNone(container.querySelector(".design-steelman"), "a failed read draws a steelman");
      assertNone(container.querySelector('nav[aria-label="where this design is"]'), "a failed read draws a stage strip");
    },
  ));

test("DesignPage: App a refused Accept shows the refusal in the question's thread, and the design is not read again", (t) => {
  const script: Script = {
    get: () => ({ response: questionsRecord(false) }),
    answer: () => ({ status: 400, code: "failed_precondition", message: "the question is no longer open" }),
  };
  return withApp(t, script, async ({ container, server, settle }) => {
    await holdTide(container);
    await settle(() => container.querySelector('[aria-label="question Q1"].qthread') !== null, "the open question is drawn");
    await press(buttonNamed(required(container, '[aria-label="question Q1"]'), "Accept"));
    await settle(
      () => container.querySelector('[aria-label="question Q1"] [role="alert"]') !== null,
      "the refusal is drawn",
    );
    assert.equal(
      required(container, '[aria-label="question Q1"] [role="alert"]').textContent,
      "[failed_precondition] the question is no longer open",
      "the question's thread does not show the refusal",
    );
    assert.deepEqual(server.gets, ["tide"], "a refused answer reads the design again");
    assert.ok(container.querySelector('[aria-label="question Q1"].qthread') !== null, "a refused question is drawn settled");
  });
});

test("DesignPage: App a held design whose listed entry changes is read again", (t) =>
  withApp(t, { get: () => ({ response: stagedRecord() }) }, async ({ container, server, settle, redraw }) => {
    await holdTide(container);
    await settle(() => container.querySelector('nav[aria-label="where this design is"]') !== null, "the held design's strip is drawn");
    await redraw([tideOf("Asking")]);
    await settle(() => server.gets.length === 2, "the changed design is read again");
    assert.deepEqual(server.gets, ["tide", "tide"], "the changed listed entry does not read the design again");
  }));

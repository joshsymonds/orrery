import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { type Client, createClient, createRouterTransport } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { ContractDesign } from "./contract";
import { ContractRecord, type ContractRecordProps } from "./contractrecord";
import { at } from "./fixtures/clock";
import {
  type ContractDecision,
  ContractDecisionSchema,
  ContractSchema,
  ContractVersionSchema,
  Contracts,
  type ContractsGetResponse,
  ContractsGetResponseSchema,
} from "./gen/orrery/v1/contracts_pb";
import { DesignLogLineSchema, type DesignQuestion, DesignQuestionSchema } from "./gen/orrery/v1/designs_pb";
import { EpicContractSchema } from "./gen/orrery/v1/epics_pb";
import type { QuestionAnswer } from "./questionthread";
import { assertNone, assertSame, installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

const id = "tidepool/tide-alerts";
const otherId = "tidepool/tide-tables";
const question = "Which alert window does the office get?";
const reason = "The office plans two days out, and nobody acts on later tables yet.";

// decision is one decision of the contract, open unless its answer is given, with the question and reason every decision
// of these tests shares.
function decision(init: MessageInitShape<typeof ContractDecisionSchema>): ContractDecision {
  return create(ContractDecisionSchema, { question, reason, row: "F1", answer: "", ...init });
}

// proposedGet is the Get of a proposed contract at version 1, whose body and decisions are the ones given. Its care is
// limited, so Accept is offered once no decision is open.
function proposedGet(
  body: MessageInitShape<typeof EpicContractSchema>,
  decisions: ContractDecision[] = [],
): ContractsGetResponse {
  return create(ContractsGetResponseSchema, {
    contract: create(ContractSchema, {
      id,
      repository: "tidepool",
      title: "Alert the office",
      care: "limited",
      state: "proposed",
      version: 1n,
      openDecisions: BigInt(decisions.filter((open) => open.answer === "").length),
      conversation: "s1",
      created: at("@14:15"),
      changed: at("@14:15"),
    }),
    versions: [
      create(ContractVersionSchema, {
        version: 1n,
        contract: create(EpicContractSchema, body),
        requirements: [],
        decisions,
        time: at("@14:15"),
        writer: "s1",
      }),
    ],
  });
}

// intentOf is an intent of n non-empty lines, so a contract that holds nothing else is n lines long.
function intentOf(n: number): string {
  return Array.from({ length: n }, (_, index) => `line ${index + 1}`).join("\n");
}

// serveGet is a fake server whose Contracts.Get answers the Get given. The record makes no other call in these tests.
function serveGet(get: ContractsGetResponse): Client<typeof Contracts> {
  const transport = createRouterTransport(({ service }) => {
    service(Contracts, {
      get: async () => get,
    });
  });
  return createClient(Contracts, transport);
}

// rendered draws the record of the Get given, with the props given on top, waits until its document has drawn, and runs
// check on its container. It removes the DOM however check ends.
async function rendered(
  t: TestContext,
  get: ContractsGetResponse,
  extra: Partial<ContractRecordProps>,
  check: (container: HTMLElement) => void,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const props: ContractRecordProps = {
    id,
    clients: { contracts: serveGet(get) },
    writer: (session) => `writer ${session}`,
    hasEpic: false,
    read: new Set(),
    onRead: () => undefined,
    live: true,
    ...extra,
  };
  try {
    await act(() => {
      render(<ContractRecord {...props} />, container);
    });
    await until(t.signal, () => container.querySelector(".contract") !== null, "the contract to draw", {
      step: () => act(turn),
    });
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// all returns the elements under root that match selector, in document order.
function all(root: ParentNode, selector: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(selector));
}

// decisionsOf is the Decisions I need from you section of the document.
function decisionsOf(container: HTMLElement): HTMLElement {
  return one(container, 'section[aria-label="Decisions I need from you"]');
}

test("ContractPage: each open decision draws its question, each option as its own row, and the recommended option marked", (t) =>
  rendered(
    t,
    proposedGet({ intent: "x" }, [
      decision({
        id: "D1",
        options: ["Alerts at once", "Alerts after two days", "Alerts never"],
        recommendation: "Alerts after two days",
      }),
    ]),
    {},
    (container) => {
      const questionRow = one(container, '.tr[data-row="D1"]');
      assert.equal(one(questionRow, ".strong").textContent, question);
      assert.equal(one(questionRow, ".wide.ink-3").textContent, reason);
      const options = all(decisionsOf(container), ".tr.option");
      assert.deepEqual(
        options.map((row) => [row.querySelector(".wide")?.textContent, row.querySelector(".chip")?.textContent ?? ""]),
        [
          ["Alerts at once", ""],
          ["Alerts after two days", "recommended"],
          ["Alerts never", ""],
        ],
      );
      assertSame(options[0]?.previousElementSibling, questionRow, "the options follow their question");
    },
  ));

test("ContractPage: a recommendation that is no option is drawn beside its question, marked recommended", (t) =>
  rendered(
    t,
    proposedGet({ intent: "x" }, [decision({ id: "D1", options: ["Yes", "No"], recommendation: "Maybe later" })]),
    {},
    (container) => {
      const questionRow = one(container, '.tr[data-row="D1"]');
      assert.equal(one(questionRow, ".chip").textContent, "recommended");
      assert.equal(one(questionRow, ".chip").parentElement?.textContent, "recommended Maybe later");
      assert.deepEqual(
        all(decisionsOf(container), ".tr.option").map((row) => row.querySelector(".chip")?.textContent ?? ""),
        ["", ""],
      );
    },
  ));

test("ContractPage: a question the design carries draws as a thread after the decisions, and answers through the design", (t) => {
  const answered: Array<[bigint, QuestionAnswer]> = [];
  const questions: DesignQuestion[] = [
    create(DesignQuestionSchema, {
      id: 1n,
      contract: id,
      text: "Keep the window?",
      options: ["Keep", "Drop"],
      recommendation: "Keep",
      asked: at("@14:20"),
    }),
    create(DesignQuestionSchema, {
      id: 2n,
      contract: otherId,
      text: "Which port?",
      options: ["A"],
      recommendation: "A",
      asked: at("@14:20"),
    }),
  ];
  const log = [
    create(DesignLogLineSchema, { seq: 1n, time: at("@14:21"), session: "s1", what: "asked", ids: ["Q1"], words: "" }),
    create(DesignLogLineSchema, { seq: 2n, time: at("@14:22"), session: "s1", what: "asked", ids: ["Q2"], words: "" }),
  ];
  const design: ContractDesign = {
    questions,
    log,
    onAnswer: async (asked, answer) => {
      answered.push([asked, answer]);
    },
  };
  const get = proposedGet({ intent: "x" }, [decision({ id: "D1", options: ["Yes", "No"], recommendation: "Yes" })]);
  return rendered(t, get, { design }, (container) => {
    const thread = one(decisionsOf(container), 'section[aria-label="question Q1"]');
    assert.equal(one(thread, ".qthread .q").textContent, "Keep the window?");
    assertNone(container.querySelector('[aria-label="question Q2"]'), "another contract's question is not drawn");
    assert.equal(all(thread, ".qt-msg").length, 1, "the thread holds the log line that names it");
    assert.equal(
      one(container, '.tr[data-row="D1"]').compareDocumentPosition(thread) & Node.DOCUMENT_POSITION_FOLLOWING,
      Node.DOCUMENT_POSITION_FOLLOWING,
      "the thread follows the decisions",
    );
    const accept = all(thread, "button").find((button) => button.textContent === "Accept");
    assert.ok(accept !== undefined, "the thread offers Accept");
    accept.click();
    assert.deepEqual(answered, [[1n, { accept: true }]]);
  });
});

test("ContractPage: a contract of 40 lines draws its count with no length word, and Accept stays enabled", (t) =>
  rendered(t, proposedGet({ intent: intentOf(40) }), {}, (container) => {
    assert.equal(one(container, ".dline .length").textContent, "40 lines");
    assert.equal(one(container, ".cmdbar button.primary").hasAttribute("disabled"), false);
  }));

test("ContractPage: a contract of 41 lines draws long, and Accept stays enabled", (t) =>
  rendered(t, proposedGet({ intent: intentOf(41) }), {}, (container) => {
    assert.equal(one(container, ".dline .length").textContent, "41 lines · long");
    assert.equal(one(container, ".cmdbar button.primary").hasAttribute("disabled"), false);
  }));

test("ContractPage: a contract of 61 lines draws very long, says the length is never a refusal, and Accept stays enabled", (t) =>
  rendered(t, proposedGet({ intent: intentOf(61) }), {}, (container) => {
    const meter = one(container, ".dline .length");
    assert.equal(meter.textContent, "61 lines · very long");
    assert.ok(meter.getAttribute("title")?.includes("never a refusal"), "the title says the length is never a refusal");
    assert.equal(one(container, ".cmdbar button.primary").hasAttribute("disabled"), false);
  }));

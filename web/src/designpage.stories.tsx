// The stories of DesignPage: a design in each stage of its strip, from research to a proposed contract and to every contract
// accepted; a record with no stage; the record still loading; and a request that failed. Every name, word and time here is
// invented.

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import * as drawing from "./designpage";
import { at } from "./fixtures/clock";
import {
  DesignApproachSchema,
  DesignChoiceSchema,
  DesignDelegationSchema,
  DesignLogLineSchema,
  DesignQuestionSchema,
  DesignSchema,
  DesignStepSchema,
  DesignsGetResponseSchema,
  RecordMakerSchema,
  type Design,
  type DesignsGetResponse,
} from "./gen/orrery/v1/designs_pb";

const noop = async () => {};
const noopOpen = () => undefined;

// scout is a research step whose report is the scout's return: its answer, the evidence it read, and what it did not read.
const scout = create(DesignStepSchema, {
  step: "step-1",
  kind: "research",
  subject: "how the export reads the tz database",
  status: "returned",
  report: JSON.stringify({
    status: "done",
    answer: "The export reads the database pinned in the vendored tree.",
    evidence: ["export/tz.go:42 reads the pinned copy", "export/tz_test.go:17 pins the version"],
    not_inspected: "the upstream release notes",
  }),
  created: at("@13:20"),
});

// unparsed is a research step whose report is not the scout's return, so it is drawn as its text.
const unparsed = create(DesignStepSchema, {
  step: "step-2",
  kind: "research",
  subject: "whether the backfill can run twice",
  status: "returned",
  report: "The backfill is idempotent on the key, as far as the first read shows.",
  created: at("@13:31"),
});

// design is the invented design with the given contracts, as the list holds it and as its record holds it.
function design(contracts: MessageInitShape<typeof DesignSchema>["contracts"] = []): Design {
  return create(DesignSchema, {
    id: "invented-design",
    title: "Invented design",
    conversation: "invented-conversation",
    created: at("@13:10"),
    state: "Being researched",
    contracts,
  });
}

// started is the conversation's line that started the research step, with the question it asked.
const started = create(DesignLogLineSchema, {
  seq: 1n,
  time: at("@13:20"),
  maker: create(RecordMakerSchema, { kind: "conversation" }),
  what: "research started",
  ids: ["step-1"],
  words: "How does the export read the tz database?",
});

// asked is the conversation's line that asked Q1, and said is the owner's message in its thread.
const asked = create(DesignLogLineSchema, {
  seq: 2n,
  time: at("@13:50"),
  maker: create(RecordMakerSchema, { kind: "conversation" }),
  what: "asked",
  ids: ["Q1"],
});

const said = create(DesignLogLineSchema, {
  seq: 3n,
  time: at("@13:52"),
  maker: create(RecordMakerSchema, { kind: "owner" }),
  what: "said",
  ids: ["Q1"],
  words: "Which of the two keeps the Lisbon case?",
});

// answered is the line that records Q1's answer, made by the owner in Orrery.
const answered = create(DesignLogLineSchema, {
  seq: 4n,
  time: at("@14:02"),
  maker: create(RecordMakerSchema, { kind: "owner" }),
  what: "answered",
  ids: ["Q1"],
  words: "Keep the pin",
});

// get is the response that holds the design's record, with the given parts of it.
function get(init: MessageInitShape<typeof DesignsGetResponseSchema>): DesignsGetResponse {
  return create(DesignsGetResponseSchema, init);
}

// DesignPage draws each stage of the strip from the record that reaches it, with the parts that record holds, and the
// three states the page draws without a record.
export const DesignPage: Stories = {
  research: () => {
    const record = design();
    return (
      <drawing.DesignPage
        design={record}
        get={get({ design: record, stage: { steps: [scout, unparsed] }, log: [started] })}
        error=""
        onOpenContract={noopOpen}
        onAnswer={noop}
      />
    );
  },
  questions: () => {
    const record = design();
    return (
      <drawing.DesignPage
        design={record}
        get={get({
          design: record,
          stage: {
            steps: [scout],
            questions: [
              create(DesignQuestionSchema, {
                id: 1n,
                contract: "rota-export",
                text: "Bump the embedded tz database?\n\nThe pinned database lacks the 2026 Lisbon rule.",
                options: ["Keep the pin", "Allow the bump"],
                recommendation: "Keep the pin",
                asked: at("@13:50"),
                answer: "Keep the pin",
                answered: at("@14:02"),
                maker: create(RecordMakerSchema, { kind: "owner_serve" }),
              }),
              create(DesignQuestionSchema, {
                id: 2n,
                contract: "rota-export",
                text: "Should the export keep the old column name?",
                options: ["Keep it", "Rename it"],
                recommendation: "Keep it",
                asked: at("@14:10"),
              }),
            ],
          },
          log: [asked, said, answered],
        })}
        error=""
        onOpenContract={noopOpen}
        onAnswer={noop}
      />
    );
  },
  design: () => {
    const record = design();
    return (
      <drawing.DesignPage
        design={record}
        get={get({
          design: record,
          stage: { steps: [scout] },
          approaches: [
            create(DesignApproachSchema, {
              seq: 1n,
              name: "Pin the database",
              shape: "Keep the vendored copy and read it at build time.",
              cost: "Stale until someone bumps it.",
              wins: "No change to the build.",
              loses: "The 2026 rule is missing.",
              research: ["export/tz.go:42"],
              recommended: true,
            }),
            create(DesignApproachSchema, {
              seq: 2n,
              name: "Read the system database",
              shape: "Read the host's copy when the export runs.",
              cost: "Output depends on the host.",
              reconsider: "When the hosts are kept current.",
            }),
          ],
          choice: create(DesignChoiceSchema, {
            approach: 1n,
            words: "Pin it for now.",
            maker: create(RecordMakerSchema, { kind: "owner_serve" }),
          }),
        })}
        error=""
        onOpenContract={noopOpen}
        onAnswer={noop}
      />
    );
  },
  steelman: () => {
    const record = design();
    return (
      <drawing.DesignPage
        design={record}
        get={get({
          design: record,
          stage: {
            steps: [
              create(DesignStepSchema, {
                step: "step-3",
                kind: "discovery",
                subject: "the four drafts",
                status: "done",
                verdict: "READY",
                counted: true,
                created: at("@14:20"),
              }),
            ],
          },
        })}
        error=""
        onOpenContract={noopOpen}
        onAnswer={noop}
      />
    );
  },
  closure: () => {
    const record = design();
    return (
      <drawing.DesignPage
        design={record}
        get={get({ design: record, stage: { closureSkipped: true } })}
        error=""
        onOpenContract={noopOpen}
        onAnswer={noop}
      />
    );
  },
  proposed: () => {
    const record = design([
      { id: "invented/first", title: "first", repository: "invented", state: "accepted", position: 1n },
      {
        id: "invented/second",
        title: "second",
        repository: "invented",
        state: "proposed",
        position: 2n,
        after: ["invented/first"],
      },
    ]);
    return (
      <drawing.DesignPage
        design={record}
        get={get({
          design: record,
          stage: { steps: [scout] },
          delegations: [
            create(DesignDelegationSchema, {
              seq: 1n,
              kinds: ["finding"],
              message: "Decide the small ones, and tell me the rest.",
              session: "invented-session",
              time: at("@13:45"),
              decisions: [
                {
                  kind: "finding",
                  ref: "D1",
                  text: "Adopted the text change to the export's header.",
                  reason: "it only needs the text changed",
                  time: at("@13:58"),
                },
              ],
            }),
          ],
          log: [started],
        })}
        error=""
        onOpenContract={noopOpen}
        onAnswer={noop}
      />
    );
  },
  accepted: () => {
    const record = design([
      { id: "invented/first", title: "first", repository: "invented", state: "accepted", position: 1n },
      {
        id: "invented/second",
        title: "second",
        repository: "invented",
        state: "accepted",
        position: 2n,
        after: ["invented/first"],
      },
    ]);
    return (
      <drawing.DesignPage
        design={record}
        get={get({ design: record, stage: { steps: [scout] } })}
        error=""
        onOpenContract={noopOpen}
        onAnswer={noop}
      />
    );
  },
  "no-stage": () => {
    const record = design();
    return (
      <drawing.DesignPage design={record} get={get({ design: record, log: [started] })} error="" onOpenContract={noopOpen} onAnswer={noop} />
    );
  },
  loading: () => <drawing.DesignPage design={design()} get={null} error="" onOpenContract={noopOpen} onAnswer={noop} />,
  error: () => (
    <drawing.DesignPage
      design={design()}
      get={null}
      error="the design did not load: orreryd is not running"
      onOpenContract={noopOpen}
      onAnswer={noop}
    />
  ),
};

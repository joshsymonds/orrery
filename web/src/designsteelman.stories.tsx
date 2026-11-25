// The stories of DesignSteelman: a design's steelman while its discovery runs; once discovery has counted, with its ledger
// open and decided in three ways; closed, with the ledger frozen; and closure skipped. Every name and word is invented.

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import type { Stories } from "./catalog/story";
import { DesignSteelman as Steelman } from "./designsteelman";
import {
  DesignLedgerItemSchema,
  DesignStageSchema,
  DesignStepSchema,
  RecordMakerSchema,
  type DesignLedgerItem,
  type DesignStage,
  type DesignStep,
} from "./gen/orrery/v1/designs_pb";

// step is an invented pass of the design, started at the given unix second.
function step(init: MessageInitShape<typeof DesignStepSchema>): DesignStep {
  return create(DesignStepSchema, init);
}

// item is an invented ledger finding of the design, with the given maker when one decided it.
function item(init: MessageInitShape<typeof DesignLedgerItemSchema>): DesignLedgerItem {
  return create(DesignLedgerItemSchema, init);
}

// stage is an invented design stage with the given steps, ledger and budget flags.
function stage(init: MessageInitShape<typeof DesignStageSchema>): DesignStage {
  return create(DesignStageSchema, init);
}

const discovery = step({
  step: "step-1",
  kind: "discovery",
  subject: "the four drafts",
  status: "done",
  verdict: "NEEDS_DECISION",
  counted: true,
  created: 1794660000n,
});

const closure = step({
  step: "step-2",
  kind: "closure",
  subject: "the revised drafts and the ledger",
  status: "done",
  verdict: "STILL_OPEN",
  counted: true,
  created: 1794672000n,
});

// The ledger of the discovered design: one finding the Director adopted for you, one still open, and one you rejected in
// the conversation, with your words quoted.
const discovered: DesignLedgerItem[] = [
  item({
    id: "D1",
    contract: "rota-export",
    origin: "discovery",
    impact: "The export drops the Lisbon rule from the rota.",
    evidence: "rota_export.go:88",
    change: "Keep the Lisbon rule in the export.",
    disposition: "adopted",
    maker: create(RecordMakerSchema, {
      kind: "conversation",
      reason: "it only needs the text changed",
      delegation: "decide the small ones",
    }),
  }),
  item({
    id: "D2",
    contract: "office-alerts",
    origin: "discovery",
    impact: "The alert names a port that is no longer on the rota.",
    evidence: "alerts.yaml:14",
    change: "Name the port from the rota.",
  }),
  item({
    id: "D3",
    contract: "change-feed",
    origin: "discovery",
    impact: "The feed replays a change twice after a restart.",
    evidence: "feed.go:211",
    change: "Drop the replayed change by its sequence.",
    disposition: "rejected",
    reason: "the feed is not restarted in the office",
    maker: create(RecordMakerSchema, { kind: "owner_conversation", quote: "Restarts do not happen in the office." }),
  }),
];

// The closed design's ledger, frozen: a finding deferred by you in Orrery with its boundary, and one that closure still
// found open, with its word and note.
const closed: DesignLedgerItem[] = [
  item({
    id: "D4",
    contract: "pilot-push",
    origin: "discovery",
    impact: "The pilot has no release steps.",
    evidence: "pilot-push/plan.md:3",
    change: "Add the release steps to the pilot.",
    disposition: "deferred",
    boundary: "the pilot is cut for the office",
    maker: create(RecordMakerSchema, { kind: "owner_serve" }),
  }),
  item({
    id: "D5",
    contract: "office-alerts",
    origin: "discovery",
    impact: "The command still runs the old binary.",
    evidence: "alerts.yaml:22",
    change: "Run the alerts binary.",
    disposition: "adopted",
    maker: create(RecordMakerSchema, { kind: "conversation", reason: "it only needs the text changed" }),
    closureWord: "still open",
    closureNote: "the command runs pilotpush",
  }),
];

export const DesignSteelman: Stories = {
  running: () => (
    <Steelman
      stage={stage({
        steps: [step({ step: "step-1", kind: "discovery", subject: "the four drafts", status: "running", created: 1794660000n })],
      })}
    />
  ),
  discovered: () => <Steelman stage={stage({ steps: [discovery], ledger: discovered, discoveryCounted: true })} />,
  closed: () => (
    <Steelman
      stage={stage({
        steps: [discovery, closure],
        ledger: closed,
        frozen: true,
        discoveryCounted: true,
        closureCounted: true,
      })}
    />
  ),
  skipped: () => (
    <Steelman
      stage={stage({
        steps: [discovery],
        ledger: [discovered[0]],
        frozen: true,
        discoveryCounted: true,
        closureSkipped: true,
      })}
    />
  ),
};

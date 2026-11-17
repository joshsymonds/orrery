// The one shared world of web/visual/scenarios.md section 2, as data. Every object the fixture answers with is a
// protobuf message built here from the generated schemas. Each constant is annotated with its message's init shape,
// so a misspelt field fails the typecheck, and the fixture encodes each message with the schema's own JSON form
// (int64 as a decimal string, defaults left out).
//
// Times are written in the spec's notation: "@13:50" is that time on 2026-11-14 UTC, "@-2d 14:41" the same time two
// days earlier. Where the spec gives no value for a field, the world leaves it at the proto default. Where a field
// must hold a value the spec leaves open (the end of a run that gives only its start), the value is marked "filler".

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import {
  EpicBuildTaskSchema,
  EpicContractSchema,
  EpicHistoryEntrySchema,
  EpicQuestionSchema,
  EpicRequirementSchema,
  EpicReleaseProofSchema,
  EpicSchema,
  EpicSessionSchema,
  EpicSliceSchema,
  EpicStepCheckSchema,
  EpicStepSchema,
  EpicsGetResponseSchema,
  EpicsHistoryResponseSchema,
  EpicsListResponseSchema,
  EvidenceRunSchema,
  type EpicsGetResponse,
  type EpicsHistoryResponse,
  type EpicsListResponse,
} from "../gen/orrery/v1/epics_pb";
import {
  ConversationItemSchema,
  ConversationStateSchema,
  EngineTextSchema,
  HostStatusResponseSchema,
  ListHeadlessResponseSchema,
  ListResponseSchema,
  PaneSchema,
  PanesListResponseSchema,
  SessionsWatchConversationResponseSchema,
  WatchConversationResponseSchema,
  type HostStatusResponse,
  type ListHeadlessResponse,
  type ListResponse,
  type PanesListResponse,
  type SessionsWatchConversationResponse,
  type WatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";

// The instant the live browser starts at, and the wireframe's local-time form of the same instant. The browser runs in
// UTC, so the two read the same.
export const liveEpoch = "2026-11-14T14:41:00Z";
export const wireframeEpoch = "2026-11-14T14:41:00";

const day0 = 1794614400n; // 2026-11-14T00:00:00Z

// at parses the spec's time notation to unix seconds: "@HH:MM[:SS]" or "@-Nd HH:MM[:SS]".
export function at(notation: string): bigint {
  const match = /^@(?:(-\d+)d )?(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(notation);
  if (match === null) {
    throw new Error(`world: not a time notation: ${notation}`);
  }
  const [, days = "0", hh = "0", mm = "0", ss = "0"] = match;
  return day0 + BigInt(days) * 86400n + BigInt(Number(hh) * 3600 + Number(mm) * 60 + Number(ss));
}

// Plain is the field-object branch of a message's init shape: plain values for its fields, with no message type.
type Plain<Init> = Extract<Init, { readonly $typeName?: undefined }>;

type EpicInit = MessageInitShape<typeof EpicSchema>;
type StepInit = MessageInitShape<typeof EpicStepSchema>;
type CheckInit = MessageInitShape<typeof EpicStepCheckSchema>;
type SliceInit = MessageInitShape<typeof EpicSliceSchema>;
type RunInit = MessageInitShape<typeof EvidenceRunSchema>;
type RunFields = Plain<RunInit>;
type StateFields = Plain<MessageInitShape<typeof ConversationStateSchema>>;
type RequirementInit = MessageInitShape<typeof EpicRequirementSchema>;
type TaskInit = MessageInitShape<typeof EpicBuildTaskSchema>;
type ContractInit = MessageInitShape<typeof EpicContractSchema>;
type ProofInit = MessageInitShape<typeof EpicReleaseProofSchema>;
type HistoryInit = MessageInitShape<typeof EpicHistoryEntrySchema>;
type QuestionInit = MessageInitShape<typeof EpicQuestionSchema>;
type SessionInit = MessageInitShape<typeof EpicSessionSchema>;
type PaneInit = MessageInitShape<typeof PaneSchema>;
type ItemInit = MessageInitShape<typeof ConversationItemSchema>;
type TextInit = MessageInitShape<typeof EngineTextSchema>;

// The commits the spec names, by their full ids (scenarios.md section 2.1 and section 2.3).
export const commit = {
  rotaBase: "3e1f2ab6b681479f7e6dc3c139194a1ad6b8025b",
  rotaRtrunk: "a41b7d0a994984ee328f27586ece2fe411cb3a73",
  rotaCsv: "3f9c2e1d1ebb1664a69ab497310da60951dcb4be",
  rotaHandler: "d84a2c96e1f0b3a75c2d9e04f6a81b3c7d5e9f20",
  rotaExport: "7c1e9aae44b1b9c7c44bf9fc9e373fe744a0778d",
  rotaRetry: "e02b6c4eb44d9510b3215d974e1e03e5ada58a06",
  holderBase: "0c7e4aa22f3584d2a720b837a5507c36476852fe",
  holderScope: "b91e1edfa49dfc460d8c98ff0e6685a01712e19b",
  holderCap: "984ef49c3bff6d10661e3548cdb3e04724b8873c",
  holderNotify: "9cc2be1e8289c469a9f34e48ecabac2bcd4516d3",
  holderDocs: "9593e0b35d9438987e69889b8d9899fbc42bcaaa",
} as const;

// evidenceLogs holds the text Evidence/Artifact returns for each run's log, by run id.
export const evidenceLogs = new Map<string, string>();

// run builds one evidence run. Its log is the run's stdout, and logBytes is that text's UTF-8 length. Fields the spec
// leaves out take the proto default; the spec's captured, ended, untimed, uncaptured run is the default source.
function run(id: string, log: string, fields: Omit<RunFields, "id" | "logBytes">): RunInit {
  if (log !== "") {
    evidenceLogs.set(id, log);
  }
  return {
    source: "captured",
    state: "ended",
    timedOut: false,
    notCaptured: "",
    judgeReason: "",
    artifacts: [],
    stderrBytes: 0n,
    item: "",
    ...fields,
    id,
    logBytes: BigInt(new TextEncoder().encode(log).length),
  };
}

// gateDefaults are the step fields section 2.3 gives to every step: the check gate and no limit.
const gateDefaults = { gateName: "check", gateLimit: 1800n, limited: false, limitKind: "", limitResets: 0n };

// cleanChecks are the five engine checks a round passes before its gate.
function cleanChecks(round: number): CheckInit[] {
  return ["schema", "head", "ancestry", "clean", "owned"].map((name) => ({ round, name, ok: true }));
}

// ---------------------------------------------------------------------------------------------------------------------
// lighthouse/rota-export (section 2.3)
// ---------------------------------------------------------------------------------------------------------------------

const rotaR1Red = run("ev-rota-r1-red", "store_test.go:27: no such table: shifts", {
  phase: "red", exit: 1, verdict: "fails", passed: true, revision: commit.rotaBase,
  created: at("@13:53:10"), ended: at("@13:53:40"),
});
const rotaR2Red = run("ev-rota-r2-red", "handler_test.go:44: got 404, want 200", {
  phase: "red", exit: 1, verdict: "fails", passed: true, revision: commit.rotaBase,
  created: at("@13:53:10"), ended: at("@13:53:42"),
});
const rotaR3Red = run("ev-rota-r3-red", "csv_test.go:19: got 0 rows, want 21", {
  phase: "red", exit: 1, verdict: "fails", passed: true, revision: commit.rotaBase,
  created: at("@13:53:10"), ended: at("@13:53:41"),
});
const rotaR4Red = run("ev-rota-r4-red", "export_test.go:88: want ErrExists, got nil", {
  phase: "red", exit: 1, verdict: "fails", passed: true, revision: commit.rotaBase,
  created: at("@14:08:00"), ended: at("@14:08:30"),
});
const rotaR5Red = run("ev-rota-r5-red", "fixtures_test.go:52: zone Europe/Lisbon: 2026 rule missing", {
  phase: "red", exit: 1, verdict: "fails", passed: true, revision: commit.rotaBase,
  created: at("@14:23:00"), ended: at("@14:23:30"),
});
const rotaR1Green = run("ev-rota-r1-green", "ok  \tlighthouse/internal/rota\t1.41s", {
  phase: "green", exit: 0, verdict: "holds", passed: true, revision: commit.rotaRtrunk,
  created: at("@14:02:00"), ended: at("@14:02:40"),
});
const rotaR2Green = run("ev-rota-r2-green", "ok  \tlighthouse/api\t0.71s", {
  phase: "green", exit: 0, verdict: "holds", passed: true, revision: commit.rotaHandler,
  created: at("@14:29:00"), ended: at("@14:29:35"),
});
const rotaR3Green = run("ev-rota-r3-green", "ok  \tlighthouse/internal/rota\t1.92s", {
  phase: "green", exit: 0, verdict: "holds", passed: true, revision: commit.rotaCsv,
  created: at("@14:11:00"), ended: at("@14:11:30"),
});

const rotaRequirements: RequirementInit[] = [
  {
    id: "R1", kind: "check", text: "Shifts are stored per keeper and week.",
    evidence: "go test ./internal/rota/ -run TestShiftStorePerKeeperWeek",
    conclusion: "TestShiftStorePerKeeperWeek passes: a stored week reads back per keeper.",
    runs: [rotaR1Red, rotaR1Green],
  },
  {
    id: "R2", kind: "check", text: "The rota endpoint serves one week of shifts.",
    evidence: "go test ./api/ -run TestRotaEndpointServesWeek",
    conclusion: "TestRotaEndpointServesWeek passes: GET /rota/{week} returns that week’s shifts.",
    runs: [rotaR2Red, rotaR2Green],
  },
  {
    id: "R3", kind: "check", text: "A week exports as CSV, one row per shift.",
    evidence: "go test ./internal/rota/ -run TestCSVOneRowPerShift",
    conclusion: "TestCSVOneRowPerShift passes: 21 stored shifts give 21 rows.",
    runs: [rotaR3Red, rotaR3Green],
  },
  {
    id: "R4", kind: "check", text: "Export refuses to overwrite an existing file.",
    evidence: "go test ./cli/ -run TestExportRefusesOverwrite",
    conclusion: "TestExportRefusesOverwrite passes: Export returns ErrExists for a path that exists.",
    runs: [rotaR4Red],
  },
  {
    id: "R5", kind: "check", text: "Fixtures cover every port zone.",
    evidence: "go test ./internal/rota/ -run TestFixturesCoverPortZones",
    conclusion: "TestFixturesCoverPortZones passes for every zone on the rota.",
    runs: [rotaR5Red],
  },
  {
    id: "R6", kind: "review", text: "Every export is written to the audit log, and the docs say so.",
    evidence: "review of cli/export.go and README.md", conclusion: "", runs: [],
  },
];

const rotaGateFailOutput =
  "$ just check\n--- FAIL: TestExportRefusesOverwrite (0.01s)\n    export_test.go:88: want ErrExists, got nil\n" +
  "    export_test.go:91: the existing file was replaced: 312 bytes became 1,840 bytes\n" +
  "FAIL\nFAIL\tlighthouse/cli\t0.38s\nok  \tlighthouse/internal/rota\t1.90s\nFAIL";
const rotaGreenGateTail = "$ just check\nok  \tlighthouse/internal/rota\t1.41s\ngolangci-lint: 0 issues";

// The round a passing implementer's gate ran in: five clean checks and a gate that passed.
const rotaPassingChecks = (round: number, tail: string): CheckInit[] => [
  ...cleanChecks(round),
  { round, name: "gate", ok: true, tailStdout: tail },
];

const rotaT5Try1: StepInit = {
  ...gateDefaults, id: "st-rota-t5-try1", task: "T5", agent: "haiku", role: "implementer", readOnly: false,
  end: "looping", status: "ended", held: false,
  started: at("@13:52:40"), ended: at("@13:58"), stage: "", stageStarted: at("@13:52:40"), doing: "",
  reason: "The monitor saw the same edit three times, so it was nudged, ended and retried on sonnet.",
  summary: "",
};
const rotaT4Impl: StepInit = {
  ...gateDefaults, id: "st-rota-t4-impl", task: "T4", agent: "haiku", role: "implementer", readOnly: false,
  end: "done", status: "ended", held: false,
  started: at("@13:52:41"), ended: at("@14:01"), stage: "", stageStarted: at("@14:00:10"), doing: "", reason: "",
  summary: "Shifts are stored per keeper and ISO week in the shifts table.",
  checks: rotaPassingChecks(1, rotaGreenGateTail),
  verdicts: [{ round: 1, verdict: "met", reason: "The requirement’s test fails at the parent and passes at the commit." }],
};
const rotaT5Try2: StepInit = {
  ...gateDefaults, id: "st-rota-t5-try2", task: "T5", agent: "sonnet", role: "implementer", readOnly: false,
  end: "done", status: "ended", held: false,
  started: at("@13:58:30"), ended: at("@14:11"), stage: "", stageStarted: at("@14:10:20"), doing: "", reason: "",
  summary: "A week exports as CSV with one row per stored shift.",
  checks: rotaPassingChecks(1, rotaGreenGateTail),
  verdicts: [{ round: 1, verdict: "met", reason: "The requirement’s test fails at the parent and passes at the commit." }],
};
const rotaT2Impl: StepInit = {
  ...gateDefaults, id: "st-rota-t2-impl", task: "T2", agent: "sonnet", role: "implementer", readOnly: false,
  end: "", status: "running", held: false,
  started: at("@14:07"), ended: 0n, stage: "turn", stageStarted: at("@14:36:48"),
  doing: "Adding the refuse-on-existing check and its test.", reason: "", summary: "",
  checks: [
    ...cleanChecks(1),
    { round: 1, name: "gate", ok: false, detail: "exit 1: cli/export_test.go:88: want ErrExists, got nil",
      stdout: rotaGateFailOutput, tailStdout: rotaGateFailOutput,
      stderr: "error: recipe `check` failed with exit code 1", tailStderr: "error: recipe `check` failed with exit code 1" },
    ...cleanChecks(2),
    { round: 2, name: "gate", ok: true, tailStdout: "$ just check\nok  \tlighthouse/cli\t0.44s\nok  \tlighthouse/internal/rota\t1.92s\ngolangci-lint: 0 issues" },
  ],
  verdicts: [{
    round: 2, verdict: "met",
    reason: "TestExportRefusesOverwrite fails at the parent commit and passes at e02b6c4, and Export returns ErrExists for a file that is there.",
  }],
};
const rotaT3Impl: StepInit = {
  ...gateDefaults, id: "st-rota-t3-impl", task: "T3", agent: "haiku", role: "implementer", readOnly: false,
  end: "done", status: "ended", held: false,
  started: at("@14:16"), ended: at("@14:30"), stage: "", stageStarted: at("@14:29:10"), doing: "", reason: "",
  summary: "GET /rota/{week} serves one week of shifts from the store.",
  checks: rotaPassingChecks(1, rotaGreenGateTail),
  verdicts: [{ round: 1, verdict: "met", reason: "The requirement’s test fails at the parent and passes at the commit." }],
};
const rotaT1Impl: StepInit = {
  ...gateDefaults, id: "st-rota-t1-impl", task: "T1", agent: "haiku", role: "implementer", readOnly: false,
  end: "", status: "held", held: true,
  started: at("@14:22"), ended: 0n, stage: "held", stageStarted: at("@14:39"),
  doing: "It asked to bump the embedded tz database. That changes go.mod, which is outside its owned files.",
  reason: "Paused by planner turn 4 for Q1: bump the embedded tz database?", summary: "",
};
const rotaT3Review: StepInit = {
  ...gateDefaults, id: "st-rota-t3-review", task: "T3", agent: "sonnet", role: "reviewer", readOnly: true,
  end: "done", status: "ended", held: false,
  started: at("@14:31"), ended: at("@14:33"), stage: "", stageStarted: at("@14:31"), doing: "", reason: "",
  summary: "Review clean: 0 findings.",
};
// st-rota-t2-review is the reviewer that found F1. Section 2.3 leaves it out of the epic's steps, and section 2.8 lists it
// in the history only.
const rotaT2Review: StepInit = {
  ...gateDefaults, id: "st-rota-t2-review", task: "T2", agent: "sonnet", role: "reviewer", readOnly: true,
  end: "gate_failed", status: "ended", held: false,
  started: at("@14:31:30"), ended: at("@14:36:00"), stage: "", stageStarted: 0n, doing: "",
  reason: "Finding F1: export overwrites an existing file; R4 says it must refuse.",
  summary: "1 finding, confirmed by a verifier.",
};

const rotaSteps: StepInit[] = [rotaT5Try1, rotaT4Impl, rotaT5Try2, rotaT2Impl, rotaT3Impl, rotaT1Impl, rotaT3Review];

const rotaTasks: TaskInit[] = [
  { id: "T1", title: "Fixtures cover every port zone.", status: "open", stage: "implement", requirements: ["R5"], red: rotaR5Red },
  {
    id: "T2", title: "Export refuses to overwrite an existing file.", status: "open", stage: "implement",
    requirements: ["R4"], red: rotaR4Red,
    reviews: [{
      attempt: 1n, gate: "review", step: "st-rota-t2-review", verdict: "fails",
      findings: [{
        id: "F1", claim: "export overwrites an existing file without asking. R4 says it must refuse.",
        location: "cli/export.go:57", step: "st-rota-t2-verify", result: "confirmed",
        reason: "Ran rota export twice into one path. The second run replaced the file.",
      }],
    }],
  },
  {
    id: "T3", title: "The rota endpoint serves one week of shifts.", status: "open", stage: "verify",
    requirements: ["R2"], red: rotaR2Red, green: rotaR2Green,
    reviews: [{ attempt: 1n, gate: "review", step: "st-rota-t3-review", verdict: "holds", findings: [] }],
  },
  {
    id: "T4", title: "Shifts are stored per keeper and week.", status: "done", stage: "verify",
    requirements: ["R1"], red: rotaR1Red, green: rotaR1Green,
    reviews: [{ attempt: 1n, gate: "review", step: "", verdict: "holds", findings: [] }],
  },
  {
    id: "T5", title: "A week exports as CSV, one row per shift.", status: "done", stage: "verify",
    requirements: ["R3"], red: rotaR3Red, green: rotaR3Green,
    reviews: [{ attempt: 2n, gate: "review", step: "", verdict: "holds", findings: [] }],
  },
  { id: "T6", title: "The README documents the export command and its audit line.", status: "open", stage: "", requirements: ["R6"] },
  { id: "T7", title: "Every export writes one line to the audit log.", status: "open", stage: "", requirements: ["R6"] },
];

const rotaSlices: SliceInit[] = [
  { task: "T5", hue: "done", redTest: true, working: true, testsPass: true, checks: true, gateAccepted: true, at: 0, held: false, errorAt: 0 },
  { task: "T4", hue: "done", redTest: true, working: true, testsPass: true, checks: true, gateAccepted: true, at: 0, held: false, errorAt: 0 },
  { task: "T2", hue: "dispatched", redTest: true, working: true, testsPass: true, checks: true, gateAccepted: false, at: 5, held: false, errorAt: 0 },
  { task: "T3", hue: "done", redTest: true, working: true, testsPass: true, checks: true, gateAccepted: true, at: 0, held: false, errorAt: 0 },
  { task: "T1", hue: "dispatched", redTest: true, working: true, testsPass: false, checks: false, gateAccepted: false, at: 3, held: true, errorAt: 0 },
];

const rotaContract: ContractInit = {
  title: "rota-export",
  approved: "Approved: export one week of the rota as CSV, without overwriting anything.",
  intent: "A CSV of the week’s shifts that I can send to the harbour office, because they will not use the rota page.",
  failures: [
    { id: "F1", text: "An export replaced a file I had edited by hand", rating: "serious", response: "refuse to overwrite (R4)" },
    { id: "F2", text: "A shift was missing from the file", rating: "serious", response: "one row per stored shift (R1, R3)" },
    { id: "F3", text: "Times were wrong for a port in another zone", rating: "limited", response: "fixtures for every port zone (R5)" },
  ],
  care: "serious",
  additions: [{ location: "cli/export.go", what: "An audit-log line for every export", why: "F1", cost: "one unit" }],
  premises: [
    { id: "P1", text: "The harbour office opens the CSV in a spreadsheet that reads UTF-8.", ifWrong: "Add a byte-order mark, or export XLSX instead." },
    { id: "P2", text: "A keeper’s week starts on Monday at 00:00 in the port’s own zone.", ifWrong: "The week boundary moves, and R2’s endpoint moves with it." },
    { id: "P3", text: "Every stored shift already names its port, so its zone can be read from it.", ifWrong: "A migration adds the port to every shift first." },
  ],
  exclusions: ["No dependency changes.", "No change to the rota page."],
  approach: "Store shifts per keeper and week, serve one week from a new endpoint, and add rota export to the CLI, which writes the week as CSV and refuses a path that already exists.",
  alternatives: [
    { alternative: "Mail the CSV from the server every Monday", whyNot: "The office asked for a file it can send on, not a mailing.", reconsider: "If the office asks for a weekly mail." },
    { alternative: "A print view of the rota page", whyNot: "They will not use the rota page.", reconsider: "If the office starts using the page." },
  ],
  releaseSteps: [
    { step: "Merge the epic branch to main", target: "lighthouse main", undo: "revert the merge" },
    { step: "Deploy lighthouse to halyard", target: "halyard", undo: "redeploy the previous build" },
  ],
  releaseThen: "Then halyard serves rota export, and the harbour office gets this week’s file.",
  checks: "just check on every return (go test ./... and golangci-lint); a red and a green evidence run for each check requirement; a review for R6.",
  base: commit.rotaBase,
  accepted: at("@13:50"),
};

const rotaEpic: EpicInit = {
  id: "lighthouse/rota-export", repository: "lighthouse", slug: "rota-export", outcome: "", changed: at("@14:40"),
  live: true, runningSteps: [rotaT2Impl, rotaT1Impl], endedSteps: 5, problem: "", source: "orreryd",
  outcomeDetail: "", waiting: false, phaseAt: at("@13:52"), seen: 0n,
};

const rotaHistory: HistoryInit[] = [
  { time: at("@13:50"), cursor: "c1", item: { case: "tell", value: { seq: 1n, text: "You accepted the contract: 6 requirements, serious care.", at: at("@13:50"), told: true } } },
  {
    time: at("@13:52"), cursor: "c2",
    item: {
      case: "turn",
      value: {
        seq: 1n, step: "st-rota-plan-1", state: "succeeded", reason: "",
        outline: "rota-store, csv-writer, rota-handler, export-cli, tz-fixtures",
        outcome: "Outlined 5 units and started rota-store and csv-writer, because the others build on them.",
        decisions: [], started: at("@13:52"), ended: at("@13:52:35"),
      },
    },
  },
  { time: at("@13:52:40"), cursor: "c3", item: { case: "step", value: rotaT5Try1 } },
  { time: at("@13:58:30"), cursor: "c4", item: { case: "step", value: rotaT5Try2 } },
  { time: at("@14:07"), cursor: "c5", item: { case: "step", value: rotaT2Impl } },
  {
    time: at("@14:10"), cursor: "c6",
    item: {
      case: "turn",
      value: {
        seq: 3n, state: "succeeded",
        outcome: "Outlined 2 more units, export-docs and rota-audit: 5 to 7. The audit requirement needed its own unit.",
        started: at("@14:10"), ended: at("@14:10:50"),
      },
    },
  },
  { time: at("@14:11"), cursor: "c7", item: { case: "evidence", value: { ...rotaR3Green, item: "R3" } } },
  { time: at("@14:22"), cursor: "c8", item: { case: "step", value: rotaT1Impl } },
  { time: at("@14:31:30"), cursor: "c9", item: { case: "step", value: rotaT2Review } },
  { time: at("@14:37"), cursor: "c10", item: { case: "tell", value: { seq: 2n, text: "Review finding F1 confirmed: export overwrites an existing file, and R4 says it must refuse. Sent back.", at: at("@14:37"), told: true } } },
  {
    time: at("@14:39"), cursor: "c11",
    item: {
      case: "turn",
      value: {
        seq: 4n, state: "succeeded",
        outcome: "Raised Q1 for you and paused tz-fixtures: bump the tz database?",
        decisions: [], started: at("@14:39"), ended: at("@14:39"),
      },
    },
  },
  { time: at("@14:40"), cursor: "c12", item: { case: "tell", value: { seq: 3n, text: "rota-handler: the engine started merging it onto the epic branch.", at: at("@14:40"), told: true } } },
];

const rotaSummary =
  "A keeper’s week of shifts becomes a file for the harbour office. Shifts are stored per keeper and week, one endpoint " +
  "serves a week, and rota export writes it as CSV, one row per shift. Export refuses to overwrite a file and writes " +
  "every run to the audit log. Fixtures cover every port zone. No dependency changes.";

const rotaGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: rotaEpic,
  repository: "/home/owner/code/lighthouse",
  summary: rotaSummary,
  questions: [{ id: "Q1", text: "Bump the embedded tz database? The pinned database lacks the 2026 Lisbon rule, so one fixture cannot pass. A bump changes go.mod, which is outside this unit’s owned files, and the contract says no dependency changes.", turn: 4n, asked: at("@14:39") } satisfies QuestionInit],
  sessions: [
    { id: "sess-rotaplan", pane: "%14", label: "rota-planning", call: "epic_get", at: at("@13:50") } satisfies SessionInit,
    { id: "sess-lamp", pane: "%11", label: "lamp-telemetry", call: "epic_get", at: at("@14:12") } satisfies SessionInit,
  ],
  contract: rotaContract,
  requirements: rotaRequirements,
  buildTasks: rotaTasks,
  steps: rotaSteps,
  slices: rotaSlices,
  releaseSteps: [
    { position: 1n, step: "Merge the epic branch to main", target: "lighthouse main", undo: "revert the merge", state: "waiting" } satisfies ProofInit,
    { position: 2n, step: "Deploy lighthouse to halyard", target: "halyard", undo: "redeploy the previous build", state: "waiting" } satisfies ProofInit,
  ],
});

// ---------------------------------------------------------------------------------------------------------------------
// orrery/orrery-followups (section 2.5a; requirement texts epicnow.js:123-127)
// ---------------------------------------------------------------------------------------------------------------------

const followupsR1Red = run("ev-fu-r1-red", "", { phase: "red", exit: 1, verdict: "fails", passed: true });
const followupsR2Red = run("ev-fu-r2-red", "", { phase: "red", exit: 1, verdict: "fails", passed: true });
const followupsR2Green = run("ev-fu-r2-green", "", { phase: "green", exit: 0, verdict: "holds", passed: true });

const followupsT1Impl: StepInit = {
  ...gateDefaults, id: "st-fu-t1-impl", task: "T1", agent: "haiku", role: "implementer", readOnly: false,
  end: "", status: "running", held: false, started: at("@14:21"), ended: 0n,
  stage: "turn", stageStarted: at("@14:39"),
  doing: "Reworking the invalid-UTF-8 test so that it fails against the old cut.", reason: "", summary: "",
  checks: [...cleanChecks(1), { round: 1, name: "gate", ok: true }],
  verdicts: [{
    round: 1, verdict: "unmet",
    reason: "The invalid-UTF-8 test passes before and after the fix, so the new cut is unproven on that path.",
  }],
};
const followupsT2Impl: StepInit = {
  ...gateDefaults, id: "st-fu-t2-impl", task: "T2", agent: "haiku", role: "implementer", readOnly: false,
  end: "done", status: "ended", held: false, started: at("@14:21:05"), ended: at("@14:40"),
  stage: "", stageStarted: 0n, doing: "", reason: "", summary: "",
  checks: [...cleanChecks(1), { round: 1, name: "gate", ok: true }],
  verdicts: [{ round: 1, verdict: "met", reason: "" }],
};
const followupsT2Review: StepInit = {
  ...gateDefaults, id: "st-fu-t2-review", task: "T2", agent: "sonnet", role: "reviewer", readOnly: true,
  end: "", status: "running", held: false, started: at("@14:40"), ended: 0n,
  stage: "turn", stageStarted: at("@14:40"), doing: "Reading runner.go:373 and the new limit test against R2.",
  reason: "", summary: "",
};

const followupsGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: {
    id: "orrery/orrery-followups", repository: "orrery", slug: "orrery-followups", outcome: "",
    changed: at("@14:40"), live: true, runningSteps: [followupsT1Impl, followupsT2Review], endedSteps: 1,
    problem: "", source: "orreryd", outcomeDetail: "", waiting: false, phaseAt: at("@14:21"), seen: 0n,
  },
  repository: "/home/owner/Personal/orrery",
  requirements: [
    {
      id: "R1", kind: "check",
      text: "The judge gets the Task and Return sections whole up to 16 KiB, else their first and last 8 KiB and a line naming the bytes left out.",
      runs: [followupsR1Red],
    },
    {
      id: "R2", kind: "check",
      text: "A planner turn freed by a rejected limit with no reset stores “reset time unknown”.",
      runs: [followupsR2Red, followupsR2Green],
    },
    { id: "R3", kind: "review", text: "docs/contract.md and engine-protocol.md state R1 and R2.", runs: [] },
  ],
  buildTasks: [
    {
      id: "T1", status: "open", stage: "implement", requirements: ["R1"],
      title: "The done-judge sees a long Task or Return section’s head and tail, with a line naming the bytes left out.",
    },
    {
      id: "T2", status: "open", stage: "review", requirements: ["R2"],
      title: "A planner turn freed by a limit with no reset says “reset time unknown”.",
    },
    { id: "T3", status: "open", stage: "", requirements: ["R3"], title: "The docs state the new judge input and the new limit text." },
  ],
  steps: [followupsT1Impl, followupsT2Impl, followupsT2Review],
  slices: [
    { task: "T1", hue: "dispatched", redTest: true, working: true, testsPass: true, checks: true, gateAccepted: false, at: 5, held: false, errorAt: 0 },
    { task: "T2", hue: "done", redTest: true, working: true, testsPass: true, checks: true, gateAccepted: true, at: 0, held: false, errorAt: 0 },
  ],
});

// ---------------------------------------------------------------------------------------------------------------------
// orrery/holder-scopes (section 2.4)
// ---------------------------------------------------------------------------------------------------------------------

const holderR1Red = run("ev-holder-r1-red", "holder_test.go:71: unit holder-1.scope not found", {
  phase: "red", exit: 1, verdict: "fails", passed: true, revision: commit.holderBase, created: at("@08:10"),
});
const holderR2Red = run("ev-holder-r2-red", "cap_test.go:38: MemoryMax unset on holder-1.scope", {
  phase: "red", exit: 1, verdict: "fails", passed: true, revision: commit.holderBase, created: at("@08:10"),
});
const holderR3Red = run("ev-holder-r3-red", "unit_test.go:19: Type=simple, want notify", {
  phase: "red", exit: 1, verdict: "fails", passed: true, revision: commit.holderBase, created: at("@08:10"),
});
const holderR1Green = run("ev-holder-r1-green", "", {
  phase: "green", exit: 0, verdict: "holds", passed: true, revision: commit.holderScope, created: at("@08:40"),
});
const holderR2Green = run("ev-holder-r2-green", "", {
  phase: "green", exit: 0, verdict: "holds", passed: true, revision: commit.holderCap, created: at("@09:02"),
});
const holderR3Green = run("ev-holder-r3-green", "", {
  phase: "green", exit: 0, verdict: "holds", passed: true, revision: commit.holderNotify, created: at("@09:15"),
});
const holderR4Green = run("ev-holder-r4-green", "", {
  source: "supplied", phase: "green", exit: 0, verdict: "holds", passed: true, revision: commit.holderDocs,
  created: at("@09:30"),
});

const holderStep = (
  n: 1 | 2 | 3 | 4,
  started: string,
  ended: string,
): StepInit => ({
  ...gateDefaults, id: `st-holder-t${n}-impl`, task: `T${n}`, agent: "haiku", role: "implementer", readOnly: false,
  end: "done", status: "ended", held: false, started: at(started), ended: at(ended), stage: "", stageStarted: 0n,
  doing: "", reason: "", summary: "",
  checks: [...cleanChecks(1), { round: 1, name: "gate", ok: true }],
  verdicts: [{ round: 1, verdict: "met", reason: "" }],
});
const holderSteps: StepInit[] = [
  holderStep(1, "@08:06", "@08:40"),
  holderStep(2, "@08:20", "@09:02"),
  holderStep(3, "@08:44", "@09:15"),
  holderStep(4, "@09:18", "@09:30"),
];

const holderReleaseStep1 =
  "Push the signed tag v0.45.0 to the orrery repository on GitHub, so the release workflow builds the binaries and attaches them to the v0.45.0 release page.";

const holderGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: {
    id: "orrery/holder-scopes", repository: "orrery", slug: "holder-scopes", outcome: "released",
    changed: at("@09:52"), live: false, runningSteps: [], endedSteps: 4, problem: "", source: "orreryd",
    outcomeDetail: "", waiting: false, phaseAt: at("@09:52"), seen: at("@09:55"),
  },
  repository: "/home/owner/Personal/orrery",
  summary:
    "Each holder runs in its own systemd scope, so a runaway one is killed alone and the terminal memory cap covers it. " +
    "orreryd starts as Type=notify with a 900 s ceiling, and the devspaces doc no longer names an orrery version.",
  sessions: [{ id: "sess-holdplan", pane: "", label: "holder-planning", call: "epic_create", at: at("@08:04") }],
  reviews: [{ position: 1n, reviewer: "final review", step: "st-holder-final-review", verdict: "holds", reason: "0 findings." }],
  report: {
    outcome: "released",
    shipped: ["R1", "R2", "R3", "R4"],
    notShipped: [],
    why: "All four requirements shipped. It ran 1 h 48 min, from acceptance at 08:04 to v0.45.0 running on vermissian at 09:52.",
  },
  contract: {
    title: "holder-scopes",
    approved: "Approved: each holder in its own systemd scope, under the terminal memory cap.",
    intent: "A runaway holder should not take the terminals down with it.",
    failures: [
      { id: "F1", text: "systemd killed orreryd while it was still starting", rating: "serious", response: "Type=notify with a 900 s ceiling (R3)" },
      { id: "F2", text: "The memory cap missed a holder", rating: "serious", response: "the cap covers holder scopes (R2)" },
    ],
    care: "serious",
    additions: [],
    premises: [
      { id: "P1", text: "Every holder is started by orreryd, never by hand.", ifWrong: "A hand-started holder runs outside the cap." },
      { id: "P2", text: "systemd on vermissian is 255 or newer.", ifWrong: "Scopes need the older property names." },
    ],
    exclusions: ["No change to how terminals are spawned."],
    approach: "Start each holder in a transient systemd scope under orrery.slice, and switch orreryd to Type=notify.",
    alternatives: [{
      alternative: "One cgroup for all holders",
      whyNot: "A runaway holder would still take its siblings down.",
      reconsider: "If scopes cost too much to start.",
    }],
    releaseSteps: [
      { step: holderReleaseStep1, target: "github, tag v0.45.0", undo: "none once someone has fetched the tag" },
      { step: "CI green on the tag", target: "github actions", undo: "rerun" },
      { step: "vermissian runs v0.45.0", target: "vermissian", undo: "switch to the previous generation" },
    ],
    releaseThen: "Then vermissian runs v0.45.0 and every holder is scoped.",
    checks: "just check; a red and a green run for R1-R3; a review for R4.",
    base: commit.holderBase,
    accepted: at("@08:04"),
  },
  requirements: [
    {
      id: "R1", kind: "check", text: "Each holder runs in its own systemd scope.",
      evidence: "go test ./internal/holder/ -run TestHolderRunsInOwnScope",
      conclusion: "TestHolderRunsInOwnScope passes.", runs: [holderR1Red, holderR1Green],
    },
    {
      id: "R2", kind: "check", text: "The terminal memory cap covers holder scopes.",
      evidence: "go test ./internal/holder/ -run TestMemoryCapCoversHolders",
      conclusion: "TestMemoryCapCoversHolders passes.", runs: [holderR2Red, holderR2Green],
    },
    {
      id: "R3", kind: "check", text: "orreryd starts as Type=notify with a 900 s ceiling.",
      evidence: "go test ./internal/unit/ -run TestOrrerydNotifyStart",
      conclusion: "TestOrrerydNotifyStart passes.", runs: [holderR3Red, holderR3Green],
    },
    {
      id: "R4", kind: "review", text: "The devspaces doc names no orrery version.",
      evidence: "review of docs/devspaces.md", conclusion: "The doc names no orrery version.", runs: [holderR4Green],
    },
  ],
  buildTasks: [
    {
      id: "T1", title: "Each holder runs in its own systemd scope.", status: "done", stage: "verify", requirements: ["R1"],
      red: holderR1Red, green: holderR1Green,
      reviews: [{ attempt: 1n, gate: "review", step: "", verdict: "holds", findings: [] }],
    },
    {
      id: "T2", title: "The terminal memory cap covers holder scopes.", status: "done", stage: "verify", requirements: ["R2"],
      red: holderR2Red, green: holderR2Green,
      reviews: [{ attempt: 1n, gate: "review", step: "", verdict: "holds", findings: [] }],
    },
    {
      id: "T3", title: "orreryd starts as Type=notify with a 900 s ceiling.", status: "done", stage: "verify", requirements: ["R3"],
      red: holderR3Red, green: holderR3Green,
      reviews: [{ attempt: 1n, gate: "review", step: "", verdict: "holds", findings: [] }],
    },
    {
      id: "T4", title: "The devspaces doc names no orrery version.", status: "done", stage: "verify", requirements: ["R4"],
      green: holderR4Green,
      reviews: [{ attempt: 1n, gate: "review", step: "", verdict: "holds", findings: [] }],
    },
  ],
  steps: holderSteps,
  slices: [1, 2, 3, 4].map((n) => ({
    task: `T${n}`, hue: "done", redTest: true, working: true, testsPass: true, checks: true, gateAccepted: true,
    at: 0, held: false, errorAt: 0,
  })),
  releaseSteps: [
    { position: 1n, step: holderReleaseStep1, target: "github, tag v0.45.0", undo: "none once someone has fetched the tag", state: "passed", evidence: "github · 09:44 · signature good", reason: "The signed tag v0.45.0 is on github.", attempt: 1n, at: at("@09:44") } satisfies ProofInit,
    { position: 2n, step: "CI green on the tag", target: "github actions", undo: "rerun", state: "passed", evidence: "run 5490 · 09:51", reason: "CI is green on the tag.", attempt: 1n, at: at("@09:51") } satisfies ProofInit,
    { position: 3n, step: "vermissian runs v0.45.0", target: "vermissian", undo: "switch to the previous generation", state: "passed", evidence: "orrery --version: orrery 0.45.0", reason: "vermissian runs v0.45.0.", attempt: 1n, at: at("@09:52") } satisfies ProofInit,
  ],
});

const holderHistory: HistoryInit[] = [
  { time: at("@08:04"), cursor: "c1", item: { case: "tell", value: { seq: 1n, text: "You accepted the contract: 4 requirements, serious care.", at: at("@08:04"), told: true } } },
  { time: at("@09:31"), cursor: "c2", item: { case: "tell", value: { seq: 2n, text: "docs-devspaces integrated: the last task.", at: at("@09:31"), told: true } } },
  { time: at("@09:38"), cursor: "c3", item: { case: "tell", value: { seq: 3n, text: "Final review of the epic branch: 0 findings.", at: at("@09:38"), told: true } } },
  { time: at("@09:39"), cursor: "c4", item: { case: "tell", value: { seq: 4n, text: "orrery started the release by itself, because the epic is clean.", at: at("@09:39"), told: true } } },
  { time: at("@09:44"), cursor: "c5", item: { case: "tell", value: { seq: 5n, text: "Tagged v0.45.0 and pushed. CI went green on the tag.", at: at("@09:44"), told: true } } },
  { time: at("@09:52"), cursor: "c6", item: { case: "tell", value: { seq: 6n, text: "Released v0.45.0. vermissian runs it.", at: at("@09:52"), told: true } } },
];

// ---------------------------------------------------------------------------------------------------------------------
// tidepool/tide-alerts (sections 2.5 and 2.5a; requirement texts epicnow.js:374-380)
// ---------------------------------------------------------------------------------------------------------------------

const nbsp = " ";

const tideGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: {
    id: "tidepool/tide-alerts", repository: "tidepool", slug: "tide-alerts", outcome: "contract written",
    changed: at("@14:15"), live: true, runningSteps: [], endedSteps: 0, problem: "", source: "orreryd",
    outcomeDetail: "", waiting: false, phaseAt: at("@14:15"), seen: 0n,
  },
  repository: "/home/owner/code/tidepool",
  summary:
    `A text the evening before any tide forecast above 4.5${nbsp}m at the harbour, so the boat can be moved before the ` +
    "slipway floods. The number comes from config only, each tide sends one alert, and last year’s tables replay with " +
    "none missed. A dry-run flag logs instead of sending, if you keep it.",
  sessions: [{ id: "sess-tideplan", pane: "", label: "tide-planning", call: "epic_create", at: at("@14:15") }],
  requirements: [
    { id: "R1", kind: "check", text: "A text goes out the evening before a tide above 4.5 m.", evidence: "go test ./alerts/ -run TestR1", conclusion: "", runs: [] },
    { id: "R2", kind: "check", text: "Last year’s tide tables replay with no alert missed.", evidence: "go test ./alerts/ -run TestR2", conclusion: "", runs: [] },
    { id: "R3", kind: "check", text: "The phone number comes from config only.", evidence: "go test ./alerts/ -run TestR3", conclusion: "", runs: [] },
    { id: "R4", kind: "check", text: "One alert per tide id.", evidence: "go test ./alerts/ -run TestR4", conclusion: "", runs: [] },
    { id: "R5", kind: "check", text: "A dry-run flag logs instead of sending.", evidence: "go test ./alerts/ -run TestR5", conclusion: "", runs: [] },
  ],
  contract: {
    title: "tide-alerts",
    approved: `Approved: a text the evening before any tide forecast above 4.5${nbsp}m at the harbour.`,
    intent: `A text the evening before a tide above 4.5${nbsp}m, because the slipway floods and the boat has to be moved.`,
    failures: [
      { id: "F1", text: "An alert went to a number that is not mine", rating: "severe", response: "the number comes from config only (R3)" },
      { id: "F2", text: "A real high tide passed with no text", rating: "serious", response: "replay of last year's tables (R2)" },
      { id: "F3", text: "Three texts arrived for one tide", rating: "limited", response: "one alert per tide id (R4)" },
    ],
    care: "severe",
    additions: [{ location: "alerts/send.go", what: "A dry-run flag that logs instead of sending", why: "F1", cost: "one flag" }],
    premises: [
      { id: "P1", text: "The harbour authority publishes tomorrow’s table by 17:00.", ifWrong: "The evening text moves to the morning." },
      { id: "P2", text: "The SMS gateway accepts one sender id for this account.", ifWrong: "Texts arrive from a shared number." },
      { id: "P3", text: "4.5 m is measured above chart datum.", ifWrong: "The threshold is off by the datum offset." },
    ],
    exclusions: ["No alerts by email or push.", "No alerts for any harbour but the home port."],
    approach: "A timer on tarn reads tomorrow’s forecast every evening at 18:00 and sends one text per tide above 4.5 m through the SMS gateway, keyed by tide id.",
    alternatives: [
      {
        alternative: "Subscribe to the harbour authority’s own alerts",
        whyNot: "They warn at 5.0 m, after the slipway floods.",
        reconsider: "If they add a 4.5 m threshold.",
      },
      { alternative: "A calendar feed of high tides", whyNot: "A calendar does not wake anyone.", reconsider: "If texts cost too much." },
    ],
    releaseSteps: [
      { step: "Send texts through the SMS gateway", target: "SMS gateway, the number in config", undo: "none, a sent text stays sent" },
      { step: "Deploy the alert job to tarn", target: "tarn", undo: "disable the timer" },
    ],
    releaseThen: "Then tarn texts you the evening before each tide above 4.5 m.",
    checks: "just check on every return; a replay of last year’s tables as the evidence for R2.",
    base: "",
    accepted: 0n,
  },
  releaseSteps: [
    { position: 1n, step: "Send texts through the SMS gateway", target: "SMS gateway, the number in config", undo: "none, a sent text stays sent", state: "waiting" } satisfies ProofInit,
    { position: 2n, step: "Deploy the alert job to tarn", target: "tarn", undo: "disable the timer", state: "waiting" } satisfies ProofInit,
  ],
});

// ---------------------------------------------------------------------------------------------------------------------
// tidepool/offline-tables and orrery/orbit-labels (section 2.5a; texts epicnow.js:394-471)
// ---------------------------------------------------------------------------------------------------------------------

// greenRun is a passing run of one requirement. Its source is supplied for a review requirement, which has no test to
// capture. Section 2.5a gives only pass and verdict for these runs.
function greenRun(id: string, review: boolean): RunInit {
  return run(id, "", { source: review ? "supplied" : "captured", phase: "green", exit: 0, verdict: "holds", passed: true });
}

const offlineGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: {
    id: "tidepool/offline-tables", repository: "tidepool", slug: "offline-tables", outcome: "stopped",
    changed: at("@14:32"), live: true, runningSteps: [], endedSteps: 0, problem: "", source: "orreryd",
    outcomeDetail: "", waiting: false, phaseAt: at("@14:32"), seen: 0n,
  },
  repository: "/home/owner/code/tidepool",
  requirements: [
    { id: "R1", kind: "check", text: "Tide tables for the home port are stored on the phone.", runs: [greenRun("ev-offline-r1-green", false)] },
    { id: "R2", kind: "check", text: "Stored tables refresh when the network returns.", runs: [greenRun("ev-offline-r2-green", false)] },
    { id: "R3", kind: "check", text: "The sync worker retries with backoff.", runs: [greenRun("ev-offline-r3-green", false)] },
    { id: "R4", kind: "check", text: "A sync never blocks the screen.", runs: [greenRun("ev-offline-r4-green", false)] },
    { id: "R5", kind: "check", text: "The tables screen works with no network.", runs: [greenRun("ev-offline-r5-green", false)] },
    { id: "R6", kind: "review", text: "The screen says how old its data is.", runs: [greenRun("ev-offline-r6-green", true)] },
  ],
  buildTasks: [
    { id: "T1", title: "Tide tables for the home port are stored on the phone.", status: "done", stage: "", requirements: ["R1", "R2"] },
    { id: "T2", title: "The sync worker retries with backoff and never blocks the screen.", status: "done", stage: "", requirements: ["R3", "R4"] },
    { id: "T3", title: "The tables screen works with no network and says how old its data is.", status: "done", stage: "", requirements: ["R5", "R6"] },
  ],
  releaseSteps: [
    {
      position: 1n, step: "Push main and tag v0.4.0, signed", target: "github, tag v0.4.0",
      undo: "none once someone has fetched the tag", state: "passed", evidence: "github · 14:05 · signature good",
      attempt: 1n, at: at("@14:05"),
    } satisfies ProofInit,
    {
      position: 2n, step: "CI green on the tag", target: "github actions", undo: "rerun", state: "failed",
      evidence: "run 5519",
      reason: "CI is red on the tag v0.4.0 for the second time: go vet: cache/key_test.go:41.",
      attempt: 2n, at: at("@14:32"),
    } satisfies ProofInit,
    { position: 3n, step: "Publish the APK to the release page", target: "github release page", undo: "delete the release asset", state: "waiting" } satisfies ProofInit,
  ],
});

const orbitGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: {
    id: "orrery/orbit-labels", repository: "orrery", slug: "orbit-labels", outcome: "releasing",
    changed: at("@14:40"), live: true, runningSteps: [], endedSteps: 0, problem: "", source: "orreryd",
    outcomeDetail: "", waiting: false, phaseAt: at("@14:37"), seen: 0n,
  },
  repository: "/home/owner/Personal/orrery",
  requirements: [
    { id: "R1", kind: "check", text: "Every orbit on the epic chart carries its stage name.", runs: [greenRun("ev-orbit-r1-green", false)] },
    { id: "R2", kind: "check", text: "Hovering a body shows its task, state and timing.", runs: [greenRun("ev-orbit-r2-green", false)] },
    { id: "R3", kind: "check", text: "The phone draws the same chart from the same cells.", runs: [greenRun("ev-orbit-r3-green", false)] },
    { id: "R4", kind: "review", text: "The chart spec states the new labels.", runs: [greenRun("ev-orbit-r4-green", true)] },
  ],
  buildTasks: [
    { id: "T1", title: "Every orbit carries its stage name.", status: "done", stage: "", requirements: ["R1"] },
    { id: "T2", title: "A card for every body, on the web and the phone.", status: "done", stage: "", requirements: ["R2", "R3"] },
    { id: "T3", title: "The chart spec states the new labels.", status: "done", stage: "", requirements: ["R4"] },
  ],
  releaseSteps: [
    // The spec leaves the target and undo of this step open ("…"), so both are empty.
    { position: 1n, step: "Merge the epic branch to main", target: "", undo: "", state: "passed", at: at("@14:38") } satisfies ProofInit,
    { position: 2n, step: "Push tag v0.46.0, signed", target: "github, tag v0.46.0", undo: "none once someone has fetched the tag", state: "running" } satisfies ProofInit,
    { position: 3n, step: "CI green on the tag", target: "github actions", undo: "rerun", state: "waiting" } satisfies ProofInit,
  ],
});

// ---------------------------------------------------------------------------------------------------------------------
// tidepool/tide-cache-drop-tables-older-than-a-week (section 2.5a; texts epicnow.js:505-512)
// ---------------------------------------------------------------------------------------------------------------------

const tidecacheGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: {
    id: "tidepool/tide-cache-drop-tables-older-than-a-week", repository: "tidepool",
    slug: "tide-cache-drop-tables-older-than-a-week", outcome: "stopped", changed: at("@09:12"), live: false,
    runningSteps: [], endedSteps: 2, problem: "", source: "orreryd", outcomeDetail: "", waiting: false,
    phaseAt: at("@09:12"), seen: 0n,
  },
  repository: "/home/owner/code/tidepool",
  requirements: [
    { id: "R1", kind: "check", text: "Tide tables are cached on disk.", runs: [] },
    { id: "R2", kind: "check", text: "The cache drops tables older than a week.", runs: [] },
    { id: "R3", kind: "review", text: "The docs describe the cache.", runs: [] },
  ],
  // The spec gives no task titles for this epic, so each task carries its requirement's text.
  buildTasks: [
    { id: "T1", title: "Tide tables are cached on disk.", status: "open", stage: "", requirements: ["R1"] },
    { id: "T2", title: "The cache drops tables older than a week.", status: "open", stage: "", requirements: ["R2"] },
    { id: "T3", title: "The docs describe the cache.", status: "open", stage: "", requirements: ["R3"] },
  ],
  report: {
    outcome: "stopped",
    shipped: [],
    notShipped: ["R1", "R2", "R3"],
    why: "You stopped it at 09:12, because offline-tables covers the same ground. Two unit branches are kept, unmerged.",
  },
});

// The Epics.List rows, in ORDER (section 2.2).
const epicRows: EpicInit[] = [
  {
    id: "orrery/orrery-followups", repository: "orrery", slug: "orrery-followups", outcome: "", changed: at("@14:40"),
    live: true, runningSteps: [followupsT1Impl, followupsT2Review], endedSteps: 1, problem: "", source: "orreryd",
    outcomeDetail: "", waiting: false, phaseAt: at("@14:21"), seen: 0n,
  },
  {
    id: "lighthouse/rota-export", repository: "lighthouse", slug: "rota-export", outcome: "", changed: at("@14:40"),
    live: true, runningSteps: [rotaT2Impl, rotaT1Impl], endedSteps: 5, problem: "", source: "orreryd",
    outcomeDetail: "", waiting: false, phaseAt: at("@13:52"), seen: 0n,
  },
  {
    id: "tidepool/tide-alerts", repository: "tidepool", slug: "tide-alerts", outcome: "contract written",
    changed: at("@14:15"), live: true, runningSteps: [], endedSteps: 0, problem: "", source: "orreryd",
    outcomeDetail: "", waiting: false, phaseAt: at("@14:15"), seen: 0n,
  },
  {
    id: "tidepool/offline-tables", repository: "tidepool", slug: "offline-tables", outcome: "stopped",
    changed: at("@14:32"), live: true, runningSteps: [], endedSteps: 0, problem: "", source: "orreryd",
    outcomeDetail: "", waiting: false, phaseAt: at("@14:32"), seen: 0n,
  },
  {
    id: "orrery/orbit-labels", repository: "orrery", slug: "orbit-labels", outcome: "releasing", changed: at("@14:40"),
    live: true, runningSteps: [], endedSteps: 0, problem: "", source: "orreryd", outcomeDetail: "", waiting: false,
    phaseAt: at("@14:37"), seen: 0n,
  },
  {
    id: "orrery/holder-scopes", repository: "orrery", slug: "holder-scopes", outcome: "released", changed: at("@09:52"),
    live: false, runningSteps: [], endedSteps: 4, problem: "", source: "orreryd", outcomeDetail: "", waiting: false,
    phaseAt: at("@09:52"), seen: at("@09:55"),
  },
  {
    id: "tidepool/tide-cache-drop-tables-older-than-a-week", repository: "tidepool",
    slug: "tide-cache-drop-tables-older-than-a-week", outcome: "stopped", changed: at("@09:12"), live: false,
    runningSteps: [], endedSteps: 2, problem: "", source: "orreryd", outcomeDetail: "", waiting: false,
    phaseAt: at("@09:12"), seen: 0n,
  },
];

// ---------------------------------------------------------------------------------------------------------------------
// Panes (section 2.6), the headless session (2.7), the host (2.9)
// ---------------------------------------------------------------------------------------------------------------------

// claudeSession is a fixed Claude session id per pane; the spec names only its shape.
const claudeSession = (pane: number) => `5e0e0000-0000-4000-8000-${String(pane).padStart(12, "0")}`;

const beaconQuestion = JSON.stringify({
  questions: [{
    question: "Which index should I add?",
    header: "Index",
    multiSelect: false,
    options: [
      { label: "Composite index", description: "On (keeper_id, shift_start). Serves the backfill and the roster page." },
      { label: "Partial index", description: "Open shifts only. Smaller, but the backfill still scans closed shifts." },
      { label: "Something else", description: "Type an answer." },
    ],
  }],
});
const lampTool = JSON.stringify({ id: "toolu_lamp_run", name: "Bash", input: "go test ./internal/telemetry/...", start: Number(at("@14:40:18")) });
const harvestTool = JSON.stringify({ id: "toolu_hv", name: "Edit", input: "report/csv.go", start: Number(at("@14:40:53")) });

const paneBeacon: PaneInit = {
  id: "%12", host: "halyard", session: "lighthouse", pane: 12, cwd: "/home/owner/code/lighthouse", command: "claude",
  state: "waiting", summary: "asks which index to add", label: "beacon-schedule", changed: at("@14:29"),
  seen: at("@14:28:30"), question: beaconQuestion, context: "", activity: at("@14:29"), tool: "",
  claudeSession: claudeSession(12), questionHash: "qh-beacon-1", newestReply: at("@14:29"), lastTurnEnd: at("@14:29"),
};
const paneLamp: PaneInit = {
  id: "%11", host: "halyard", session: "lighthouse", pane: 11, cwd: "/home/owner/code/lighthouse", command: "claude",
  state: "working", summary: "adding retry backoff", label: "lamp-telemetry", changed: at("@14:31:20"),
  seen: at("@14:31:30"), question: "", context: "", activity: at("@14:31:20"), tool: lampTool,
  claudeSession: claudeSession(11), questionHash: "", newestReply: at("@14:31:20"), lastTurnEnd: 0n,
};
const paneGraft: PaneInit = {
  id: "%13", host: "sextant", session: "orchard", pane: 13, cwd: "/home/owner/code/orchard", command: "claude",
  state: "done",
  summary: "All 48 parser tests pass. The grafting rule for nested scions was the only real change; the rest was test data.",
  label: "graft-parser", changed: at("@14:21"), seen: at("@14:00"), question: "", context: "", activity: at("@14:21"),
  tool: "", claudeSession: claudeSession(13), questionHash: "", newestReply: at("@14:21"), lastTurnEnd: at("@14:21"),
};
const paneMoon: PaneInit = {
  id: "%15", host: "halyard", session: "almanac", pane: 15, cwd: "/home/owner/code/almanac", command: "claude",
  state: "done",
  summary: "The phase endpoint now returns ISO dates, and both clients that parsed the old format are updated.",
  label: "moon-phase-api", changed: at("@14:02"), seen: at("@13:30"), question: "", context: "", activity: at("@14:02"),
  tool: "", claudeSession: claudeSession(15), questionHash: "", newestReply: at("@14:02"), lastTurnEnd: at("@14:02"),
};
const paneHarvest: PaneInit = {
  id: "%16", host: "sextant", session: "orchard", pane: 16, cwd: "/home/owner/code/orchard", command: "claude",
  state: "working", summary: "rendering CSV export", label: "harvest-report", changed: at("@14:38"),
  seen: at("@14:38:30"), question: "", context: "", activity: at("@14:38"), tool: harvestTool,
  claudeSession: claudeSession(16), questionHash: "", newestReply: at("@14:38"), lastTurnEnd: 0n,
};
const paneRota: PaneInit = {
  id: "%14", host: "halyard", session: "lighthouse", pane: 14, cwd: "/home/owner/code/lighthouse", command: "claude",
  state: "done",
  summary: "The contract for rota-export is accepted and the epic is created. I can read it or steer it from here whenever you ask.",
  label: "rota-planning", changed: at("@13:50"), seen: at("@13:51"), question: "", context: "", activity: at("@13:50"),
  tool: "", claudeSession: claudeSession(14), questionHash: "", newestReply: at("@13:50"), lastTurnEnd: at("@13:50"),
};
const paneDocs: PaneInit = {
  id: "%17", host: "tarn", session: "tidepool", pane: 17, cwd: "/home/owner/code/tidepool", command: "claude",
  state: "done", summary: "README examples fixed and run in CI.", label: "docs-examples", changed: at("@09:41"),
  seen: at("@09:42"), question: "", context: "", activity: at("@09:41"), tool: "", claudeSession: claudeSession(17),
  questionHash: "", newestReply: at("@09:41"), lastTurnEnd: at("@09:41"),
};
const paneTide: PaneInit = {
  id: "%18", host: "tarn", session: "tidepool", pane: 18, cwd: "/home/owner/code/tidepool", command: "claude",
  state: "done", summary: "Migration merged; the old table is dropped.", label: "tide-tables", changed: at("@-2d 14:41"),
  seen: at("@-2d 14:42"), question: "", context: "", activity: at("@-2d 14:41"), tool: "",
  claudeSession: claudeSession(18), questionHash: "", newestReply: at("@-2d 14:41"), lastTurnEnd: at("@-2d 14:41"),
};
const paneCi: PaneInit = {
  id: "%19", host: "halyard", session: "quarry", pane: 19, cwd: "/home/owner/code/quarry", command: "claude",
  state: "done", summary: "Cache keys trimmed to the lockfile hash.", label: "ci-cache", changed: at("@-3d 14:41"),
  seen: at("@-3d 14:42"), question: "", context: "", activity: at("@-3d 14:41"), tool: "",
  claudeSession: claudeSession(19), questionHash: "", newestReply: at("@-3d 14:41"), lastTurnEnd: at("@-3d 14:41"),
};

// ---------------------------------------------------------------------------------------------------------------------
// Conversations (section 2.10)
// ---------------------------------------------------------------------------------------------------------------------

// call is one tool call and, unless result is null, the result that follows it. A call's id is toolu_<n>.
function call(n: number, time: bigint, tool: string, input: string, result: string | null = "", error = false): ItemInit[] {
  const id = `toolu_${n}`;
  const items: ItemInit[] = [{ kind: "tool", time, id, tool, input }];
  if (result !== null) {
    items.push({ kind: "result", time, id, text: result, error });
  }
  return items;
}

const prompt = (time: bigint, sender: string, text: string, round = 0): ItemInit => ({ kind: "prompt", time, sender, text, round });
const reply = (time: bigint, text: string): ItemInit => ({ kind: "reply", time, text });

const lampReply = [
  "Backoff is in and the schedule test passes. Delays double from 250 ms and stop growing at 30 s:",
  "",
  "```go",
  "func (b Backoff) Delay(n int) time.Duration {",
  "\td := b.Base << n",
  "\tif d > b.Max || d <= 0 {",
  "\t\treturn b.Max",
  "\t}",
  "\treturn d",
  "}",
  "```",
  "",
  "`Flush` now retries a failed batch in place. Still open: there is no jitter, so lamps that drop together retry together, and retries never give up.",
].join("\n");

const lampLongBash =
  "go test ./internal/telemetry/... -run 'Backoff|Jitter|RetryWindow|SpoolAfterWindow|FlushRetriesInPlace' -count=1 -race -timeout 120s";

const lampItems: ItemInit[] = [
  {
    kind: "compaction", time: at("@14:02"), trigger: "auto", tokens: 611000n,
    text: "The telemetry uploader lost readings whenever the collector was unreachable. Established so far: Flush is the only caller of send; a batch holds at most 500 readings; the spool directory exists but nothing writes to it. Agreed plan: retry with capped exponential backoff, then spool.",
  },
  ...call(1, at("@14:03"), "Read", "internal/telemetry/flush.go"),
  ...call(2, at("@14:05"), "Write", "internal/telemetry/backoff.go"),
  ...call(3, at("@14:08"), "Bash", "go test ./internal/telemetry/...",
    "--- FAIL: TestBackoffSchedule (0.00s)\n    backoff_test.go:22: want 250ms, got 0s\nFAIL", true),
  ...call(4, at("@14:12"), "Write", "internal/telemetry/backoff_test.go"),
  ...call(5, at("@14:18"), "Bash", "go test ./internal/telemetry/...", "ok  \tlighthouse/internal/telemetry\t0.31s"),
  ...call(6, at("@14:24"), "Edit", "internal/telemetry/flush.go"),
  reply(at("@14:27"), lampReply),
  prompt(at("@14:31"), "owner", "Cap the total retry window at 5 minutes and add jitter."),
  ...call(7, at("@14:32"), "Edit", "internal/telemetry/backoff.go", "Added 14 lines, removed 3 lines"),
  ...call(8, at("@14:32:20"), "Bash", lampLongBash, "ok  \tlighthouse/internal/telemetry\t0.44s"),
];

const beaconReply = [
  "The nightly backfill scans every shift because the rotation table has no index on `keeper_id`. Two ways to fix it:",
  "",
  "1. Composite index on `(keeper_id, shift_start)`",
  "2. Partial index on open shifts only",
].join("\n");

const beaconItems: ItemInit[] = [
  prompt(at("@14:24"), "owner", "The nightly backfill takes forty minutes. Find out why and fix it."),
  ...call(1, at("@14:24:30"), "Grep", "keeper_id"),
  ...call(2, at("@14:25"), "Read", "internal/rota/backfill.go"),
  ...call(3, at("@14:26"), "Read", "migrations/0007_rotation.sql"),
  ...call(4, at("@14:27"), "Read", "internal/rota/query.go"),
  ...call(5, at("@14:28"), "Bash", 'psql -c "EXPLAIN ANALYZE SELECT * FROM rotation WHERE keeper_id = 42"',
    "Seq Scan on rotation (cost=0.00..48210.00 rows=1 width=64)"),
  reply(at("@14:29"), beaconReply),
];

const rotaPlanningItems: ItemInit[] = [
  prompt(at("@13:50"), "owner", "Why did csv-writer need a retry?"),
  ...call(1, at("@13:50:05"), "epic_get", "rota-export", "rota-export · building · 2 of 7 units integrated"),
  reply(at("@13:50:20"),
    "Its first try, on haiku, made the same edit three times, so the monitor ended it at 13:58 and the planner retried on sonnet. The retry integrated at 3f9c2e1, which proves R3."),
];

const workerBrief = [
  "Brief from planner turn 2.",
  "Goal: R4, export refuses to overwrite an existing file.",
  "Owned files: cli/export.go, cli/export_test.go.",
  "Acceptance: TestExportRefusesOverwrite fails at 3e1f2ab and passes at your commit, and just check exits 0.",
].join("\n");
const workerGateNote =
  "Gate exit 1. Sent back with the gate's output.\n" + rotaGateFailOutput + "\nerror: recipe `check` failed with exit code 1";
const workerFindingNote =
  "Review finding F1, confirmed: export overwrites an existing file without asking. R4 says it must refuse.\n" +
  "cli/export.go:57 · a verifier reproduced it at 14:36 and sent it back into this session.";
const workerLongBash =
  "go test ./cli/... ./internal/rota/... -run 'TestExportRefusesOverwrite|TestExportRefusesSecondRun|TestExportWritesAuditLine' -count=1 -v";

const workerItems: ItemInit[] = [
  prompt(at("@14:07"), "brief", workerBrief),
  ...call(1, at("@14:07:30"), "Read", "cli/export.go"),
  ...call(2, at("@14:08:30"), "Read", "cli/root.go"),
  ...call(3, at("@14:09:30"), "Read", "internal/rota/csv.go"),
  ...call(4, at("@14:10:30"), "Write", "cli/export_test.go"),
  ...call(5, at("@14:12:00"), "Write", "cli/export.go"),
  ...call(6, at("@14:13:30"), "Edit", "cli/export.go"),
  ...call(7, at("@14:15:00"), "Edit", "cli/root.go"),
  ...call(8, at("@14:16:30"), "Bash", "go test ./cli/..."),
  ...call(9, at("@14:18:30"), "Bash", 'git commit -am "export: write a week as CSV"'),
  prompt(at("@14:19"), "engine", workerGateNote, 1),
  ...call(10, at("@14:20"), "Read", "cli/export.go"),
  ...call(11, at("@14:23"), "Edit", "cli/export.go"),
  ...call(12, at("@14:26"), "Edit", "cli/export_test.go"),
  ...call(13, at("@14:30"), "Bash", "just check"),
  prompt(at("@14:36:48"), "engine", workerFindingNote),
  ...call(14, at("@14:38:10"), "Edit", "cli/export.go", "Added 9 lines, removed 1 line"),
  ...call(15, at("@14:40:52"), "Bash", workerLongBash, null),
];

const paneStreams = new Map<string, WatchConversationResponse>();
const sessionStreams = new Map<string, SessionsWatchConversationResponse>();

function paneStream(
  id: string,
  pane: PaneInit,
  items: ItemInit[],
  state: Omit<StateFields, "pane">,
  unfinished: TextInit[] = [],
): WatchConversationResponse {
  return create(WatchConversationResponseSchema, {
    transcript: `${id}.jsonl`, seq: 1n, reset: true, items, unfinished,
    state: { pane, ...state },
  });
}

paneStreams.set("%11", paneStream("%11", paneLamp, lampItems, {
  origin: "",
  goal: "Stop the telemetry uploader from losing readings when the collector is unreachable.",
  waiting: [
    { text: "Also log the batch id whenever a batch is spooled.", time: at("@14:36:10"), canceled: false },
    { text: "Then put the 5-minute cap in the README.", time: at("@14:38:40"), canceled: false },
  ],
  context: { tokens: 30000n, window: 200000n },
}, [{
  messageId: "msg_lamp_live", index: 0, parentToolUseId: "", kind: "text",
  text: "Full jitter is in: each delay is drawn uniformly between zero and the capped value. Now wiring the 5-minute window into Flush, so a batch that outlives it goes to the on-disk spool instead of being dro",
}]));

paneStreams.set("%12", paneStream("%12", paneBeacon, beaconItems, {
  goal: "", waiting: [], context: { tokens: 18000n, window: 200000n },
}));

paneStreams.set("%14", paneStream("%14", paneRota, rotaPlanningItems, { waiting: [] }));

// The other panes hold one reply: their summary, at the time they changed.
for (const [id, pane, changed] of [
  ["%13", paneGraft, at("@14:21")],
  ["%15", paneMoon, at("@14:02")],
  ["%16", paneHarvest, at("@14:38")],
  ["%17", paneDocs, at("@09:41")],
  ["%18", paneTide, at("@-2d 14:41")],
  ["%19", paneCi, at("@-3d 14:41")],
] as const) {
  paneStreams.set(id, paneStream(id, pane, [reply(changed, pane.summary ?? "")], {}));
}

sessionStreams.set("st-rota-t2-impl", create(SessionsWatchConversationResponseSchema, {
  transcript: "st-rota-t2-impl.jsonl",
  seq: 1n,
  reset: true,
  items: workerItems,
  state: {
    goal: "",
    waiting: [],
    context: { tokens: 88000n, window: 200000n },
    session: {
      id: "st-rota-t2-impl", dir: "/home/owner/code/lighthouse", label: "T2", state: "working",
      changed: at("@14:36:48"), seen: 0n, summary: "", question: "", newestReply: at("@14:40:40"),
    },
  },
  unfinished: [{
    messageId: "msg_t2_live", index: 0, parentToolUseId: "", kind: "text",
    text: "Export now checks for the file before it opens it, and returns ErrExists. Writing the test that proves a second export refus",
  }],
}));

// ---------------------------------------------------------------------------------------------------------------------
// The world the fixture serves
// ---------------------------------------------------------------------------------------------------------------------

export interface World {
  readonly epics: EpicsListResponse;
  readonly gets: ReadonlyMap<string, EpicsGetResponse>;
  readonly histories: ReadonlyMap<string, EpicsHistoryResponse>;
  readonly panes: PanesListResponse;
  readonly sessions: ListResponse;
  readonly headless: ListHeadlessResponse;
  readonly host: HostStatusResponse;
  readonly paneStreams: ReadonlyMap<string, WatchConversationResponse>;
  readonly sessionStreams: ReadonlyMap<string, SessionsWatchConversationResponse>;
  readonly evidenceLogs: ReadonlyMap<string, string>;
}

export const world: World = {
  epics: create(EpicsListResponseSchema, { epics: epicRows }),
  gets: new Map<string, EpicsGetResponse>([
    ["orrery/orrery-followups", followupsGet],
    ["lighthouse/rota-export", rotaGet],
    ["tidepool/tide-alerts", tideGet],
    ["tidepool/offline-tables", offlineGet],
    ["orrery/orbit-labels", orbitGet],
    ["orrery/holder-scopes", holderGet],
    ["tidepool/tide-cache-drop-tables-older-than-a-week", tidecacheGet],
  ]),
  histories: new Map<string, EpicsHistoryResponse>([
    ["lighthouse/rota-export", create(EpicsHistoryResponseSchema, { entries: rotaHistory, next: "" })],
    ["orrery/holder-scopes", create(EpicsHistoryResponseSchema, { entries: holderHistory, next: "" })],
  ]),
  panes: create(PanesListResponseSchema, {
    panes: [paneBeacon, paneLamp, paneGraft, paneMoon, paneHarvest, paneRota, paneDocs, paneTide, paneCi],
  }),
  sessions: create(ListResponseSchema, { sessions: [], seq: 0n }),
  headless: create(ListHeadlessResponseSchema, {
    sessions: [{
      id: "4be0c6d2-8f3a-4c71-9e25-ephemeris001", dir: "/home/owner/code/almanac", label: "ephemeris-import",
      state: "interrupted", changed: at("@12:41"), seen: at("@12:42"),
      summary: "orreryd restarted during the turn, and the turn stopped where it was.", question: "",
      newestReply: at("@12:40:30"),
    }],
  }),
  host: create(HostStatusResponseSchema, {
    host: "vermissian",
    stats: [
      { label: "cpu", value: "34%", percent: 34, level: "ok" },
      { label: "mem", value: "61%", percent: 61, level: "ok" },
      { label: "load", value: "2.1", level: "" },
      { label: "disk", value: "48%", percent: 48, level: "ok" },
    ],
  }),
  paneStreams,
  sessionStreams,
  evidenceLogs,
};

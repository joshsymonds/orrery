// The epics the fixture slices hold: the epic list in its order, the Epics.Get detail of each epic that has one, the
// Epics.History of the epics that have a history, and the log each evidence run's Evidence.Artifact answers with.

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
} from "../gen/orrery/v1/epics_pb";
import { at, commit } from "./clock";

// Plain is the field-object branch of a message's init shape: plain values for its fields, with no message type.
type Plain<Init> = Extract<Init, { readonly $typeName?: undefined }>;

type EpicInit = MessageInitShape<typeof EpicSchema>;
type StepInit = MessageInitShape<typeof EpicStepSchema>;
type CheckInit = MessageInitShape<typeof EpicStepCheckSchema>;
type SliceInit = MessageInitShape<typeof EpicSliceSchema>;
type RunInit = MessageInitShape<typeof EvidenceRunSchema>;
type RunFields = Plain<RunInit>;
type RequirementInit = MessageInitShape<typeof EpicRequirementSchema>;
type TaskInit = MessageInitShape<typeof EpicBuildTaskSchema>;
type ContractInit = MessageInitShape<typeof EpicContractSchema>;
type ProofInit = MessageInitShape<typeof EpicReleaseProofSchema>;
type HistoryInit = MessageInitShape<typeof EpicHistoryEntrySchema>;
type QuestionInit = MessageInitShape<typeof EpicQuestionSchema>;
type SessionInit = MessageInitShape<typeof EpicSessionSchema>;

// evidenceLogs holds the text Evidence/Artifact returns for each run's log, by run id.
export const evidenceLogs = new Map<string, string>();

// run builds one evidence run. Its log is the run's stdout, and logBytes is that text's UTF-8 length. Fields left out
// take the proto default, and the captured, ended, untimed, uncaptured run is the default source.
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

// gateDefaults are the step fields every step carries: the check gate and no limit.
const gateDefaults = { gateName: "check", gateLimit: 1800n, limited: false, limitKind: "", limitResets: 0n };

// cleanChecks are the five engine checks a round passes before its gate.
function cleanChecks(round: number): CheckInit[] {
  return ["schema", "head", "ancestry", "clean", "owned"].map((name) => ({ round, name, ok: true }));
}

// ---------------------------------------------------------------------------------------------------------------------
// lighthouse/rota-export
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

export const rotaGateFailOutput =
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
// st-rota-t2-review is the reviewer that found F1. The epic's steps leave it out, and its history lists it only.
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
// orrery/orrery-followups
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
// orrery/holder-scopes
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
// tidepool/tide-alerts
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
// tidepool/offline-tables and orrery/orbit-labels
// ---------------------------------------------------------------------------------------------------------------------

// greenRun is a passing run of one requirement. Its source is supplied for a review requirement, which has no test to
// capture. These runs carry only a pass and a verdict.
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
    // The target and undo of this step are left open, so both are empty.
    { position: 1n, step: "Merge the epic branch to main", target: "", undo: "", state: "passed", at: at("@14:38") } satisfies ProofInit,
    { position: 2n, step: "Push tag v0.46.0, signed", target: "github, tag v0.46.0", undo: "none once someone has fetched the tag", state: "running" } satisfies ProofInit,
    { position: 3n, step: "CI green on the tag", target: "github actions", undo: "rerun", state: "waiting" } satisfies ProofInit,
  ],
});

// ---------------------------------------------------------------------------------------------------------------------
// tidepool/tide-cache-drop-tables-older-than-a-week
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
  // This epic has no task titles, so each task carries its requirement's text.
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

// The Epics.List rows, in order.
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

// epics is the Epics.List answer: the epic rows, in the order the list draws them.
export const epics = create(EpicsListResponseSchema, { epics: epicRows });

// gets holds the Epics.Get answer of each epic that has one, by epic id.
export const gets = new Map<string, EpicsGetResponse>([
  ["orrery/orrery-followups", followupsGet],
  ["lighthouse/rota-export", rotaGet],
  ["tidepool/tide-alerts", tideGet],
  ["tidepool/offline-tables", offlineGet],
  ["orrery/orbit-labels", orbitGet],
  ["orrery/holder-scopes", holderGet],
  ["tidepool/tide-cache-drop-tables-older-than-a-week", tidecacheGet],
]);

// histories holds the Epics.History answer of each epic that has one, by epic id.
export const histories = new Map<string, EpicsHistoryResponse>([
  ["lighthouse/rota-export", create(EpicsHistoryResponseSchema, { entries: rotaHistory, next: "" })],
  ["orrery/holder-scopes", create(EpicsHistoryResponseSchema, { entries: holderHistory, next: "" })],
]);

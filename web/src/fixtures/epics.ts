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
import { at, commit } from "./clock";

// Plain is the field-object branch of a message's init shape: plain values for its fields, with no message type.
type Plain<Init> = Extract<Init, { readonly $typeName?: undefined }>;

type EpicInit = MessageInitShape<typeof EpicSchema>;
// EpicRow is an epic's Epics.List row as a plain object, the shape of each row the page scenarios change.
export type EpicRow = Plain<EpicInit>;
type StepInit = MessageInitShape<typeof EpicStepSchema>;
type CheckInit = MessageInitShape<typeof EpicStepCheckSchema>;
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

// EvidenceArtifactBytes is what Evidence/Artifact returns for one named artifact of a run.
export interface EvidenceArtifactBytes {
  readonly data: Uint8Array;
  readonly mediaType: string;
}

// evidenceArtifactKey names one artifact of one run in evidenceArtifacts.
export function evidenceArtifactKey(run: string, path: string): string {
  return `${run}/${path}`;
}

// evidenceArtifacts holds the bytes Evidence/Artifact returns for each kept artifact, by evidenceArtifactKey.
export const evidenceArtifacts = new Map<string, EvidenceArtifactBytes>();

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
// The green run of R4 is still capturing, so it has no verdict and has not passed.
const rotaR4Green = run("ev-rota-r4-green", "", {
  phase: "green", state: "capturing", passed: false, revision: commit.rotaRetry, created: at("@14:38"),
});
// rotaR4Judging is R4's green run while a judge reads it: the change kept a screenshot, and its trace is over the cap.
const rotaR4Judging = run(
  "ev-rota-r4-judging",
  "=== RUN   TestExportRefusesAnExistingFile\n--- PASS: TestExportRefusesAnExistingFile (0.04s)\nok  \tlighthouse/internal/rota\t0.88s",
  {
  phase: "green", state: "judging", passed: false, revision: commit.rotaRetry, created: at("@14:38"),
  artifacts: [
    { path: "shots/export-refused.png", state: "kept", size: 49152n, mediaType: "image/png" },
    { path: "trace.zip", state: "over_cap", size: 52428800n, mediaType: "application/zip" },
  ],
  },
);
// The screenshot R4's judged run kept: the export page refusing a file that is already there.
evidenceArtifacts.set(evidenceArtifactKey("ev-rota-r4-judging", "shots/export-refused.png"), {
  data: new TextEncoder().encode(
    '<svg xmlns="http://www.w3.org/2000/svg" width="640" height="200" viewBox="0 0 640 200">' +
      '<rect x="24" y="24" width="592" height="152" rx="8" fill="none" stroke="currentColor"/>' +
      '<text x="48" y="80" font-family="sans-serif" font-size="20">Export</text>' +
      '<text x="48" y="124" font-family="sans-serif" font-size="16">shifts.csv already exists. Nothing was written.</text>' +
      "</svg>",
  ),
  mediaType: "image/svg+xml",
});

// rotaR4Requirement is R4 with its green run still capturing, as the shipped rota-export draws it.
const rotaR4Requirement: Plain<RequirementInit> = {
  id: "R4", kind: "check", text: "Export refuses to overwrite an existing file.",
  evidence: "go test ./cli/ -run TestExportRefusesOverwrite",
  conclusion: "TestExportRefusesOverwrite passes: Export returns ErrExists for a path that exists.",
  runs: [rotaR4Red, rotaR4Green],
};

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
  rotaR4Requirement,
  {
    id: "R5", kind: "check", text: "Fixtures cover every port zone.",
    evidence: "go test ./internal/rota/ -run TestFixturesCoverPortZones",
    conclusion: "TestFixturesCoverPortZones passes for every zone on the rota.",
    runs: [rotaR5Red],
  },
  {
    id: "R6", kind: "review", text: "Every export is written to the audit log.",
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

// The seven tasks of the rota-export plan, by task id. T6 is the readme and T7 the audit-log task, both for R6.
const rotaTasks: TaskInit[] = [
  {
    id: "T1", title: "tz-fixtures", status: "open", stage: "implement", requirements: ["R5"], red: rotaR5Red,
    place: "implement", tone: "mine",
  },
  {
    id: "T2", title: "export-cli", status: "open", stage: "implement",
    requirements: ["R4"], red: rotaR4Red, green: rotaR4Green, place: "implement", tone: "run",
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
    id: "T3", title: "rota-endpoint", status: "done", stage: "verify",
    requirements: ["R2"], red: rotaR2Red, green: rotaR2Green, place: "integrated", tone: "ok",
    reviews: [{ attempt: 1n, gate: "review", step: "st-rota-t3-review", verdict: "holds", findings: [] }],
  },
  {
    id: "T4", title: "shift-store", status: "done", stage: "verify",
    requirements: ["R1"], red: rotaR1Red, green: rotaR1Green, place: "integrated", tone: "ok",
    reviews: [{ attempt: 1n, gate: "review", step: "", verdict: "holds", findings: [] }],
  },
  {
    id: "T5", title: "csv-writer", status: "done", stage: "verify",
    requirements: ["R3"], red: rotaR3Red, green: rotaR3Green, place: "integrate", tone: "acc",
    reviews: [{ attempt: 2n, gate: "review", step: "", verdict: "holds", findings: [] }],
  },
  { id: "T6", title: "readme", status: "open", stage: "", requirements: ["R6"], place: "planned", tone: "plan" },
  { id: "T7", title: "audit-log", status: "open", stage: "", requirements: ["R6"], place: "planned", tone: "plan" },
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

const rotaEpic: Plain<EpicInit> = {
  id: "lighthouse/rota-export", repository: "lighthouse", path: "/home/owner/code/lighthouse", slug: "rota-export", steering: "sess-rota-14", outcome: "accepted", changed: at("@14:40"),
  live: true, runningSteps: [rotaT2Impl, rotaT1Impl], endedSteps: 5, source: "orreryd",
  phaseAt: at("@13:52"), seen: 0n,
};

const rotaHistory: HistoryInit[] = [
  { time: at("@13:50"), cursor: "c1", item: { case: "tell", value: { seq: 1n, text: "You accepted the contract: 6 requirements, serious care.", at: at("@13:50"), told: true } } },
  {
    time: at("@13:52"), cursor: "c2",
    item: {
      case: "turn",
      value: {
        seq: 1n, step: "st-rota-plan-1", state: "succeeded", reason: "",
        outline: "shift-store, rota-endpoint, csv-writer, export-cli, tz-fixtures",
        outcome: "Outlined 5 units and started shift-store and csv-writer, because the others build on them.",
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
        outcome: "Outlined 2 more units, readme and audit-log: 5 to 7. The audit requirement needed its own unit.",
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
  { time: at("@14:40"), cursor: "c12", item: { case: "tell", value: { seq: 3n, text: "rota-endpoint: the engine started merging it onto the epic branch.", at: at("@14:40"), told: true } } },
];

const rotaSummary =
  "A keeper’s week of shifts becomes a file for the harbour office. Shifts are stored per keeper and week, one endpoint " +
  "serves a week, and rota export writes it as CSV, one row per shift. Export refuses to overwrite a file and writes " +
  "every run to the audit log. Fixtures cover every port zone. No dependency changes.";

// rotaGetInit is rota-export's Epics.Get answer as its init, which the states the scenarios draw change.
const rotaGetInit: Plain<MessageInitShape<typeof EpicsGetResponseSchema>> = {
  epic: rotaEpic,
  repository: "/home/owner/code/lighthouse",
  summary: rotaSummary,
  questions: [{
    id: "Q1", text: "Bump the embedded tz database? The pinned database lacks the 2026 Lisbon rule, so one fixture cannot pass. A bump changes go.mod, which is outside this unit’s owned files, and the contract says no dependency changes.",
    turn: 4n, asked: at("@14:39"),
    choices: [
      { id: "Keep the pin", does: "Skip the Lisbon case. The contract holds: R5 is met for every port on today’s rota." },
      { id: "Allow the bump", does: "A contract change. Adds go.mod to this task and reruns the full gate." },
    ],
    recommendation: { choice: "Keep the pin", why: "Lisbon is not on any rota until March.", confidence: "high" },
  } satisfies QuestionInit],
  sessions: [
    { id: "sess-rotaplan", pane: "%14", label: "Planned the rota export, and steers its epic", call: "epic_get", at: at("@13:50") } satisfies SessionInit,
    { id: "sess-lamp", pane: "%11", label: "Retry backoff and jitter for the telemetry uploader", call: "epic_get", at: at("@14:12") } satisfies SessionInit,
  ],
  contract: rotaContract,
  requirements: rotaRequirements,
  buildTasks: rotaTasks,
  steps: rotaSteps,
  releaseSteps: [
    { position: 1n, step: "Merge the epic branch to main", target: "lighthouse main", undo: "revert the merge", state: "waiting" } satisfies ProofInit,
    { position: 2n, step: "Deploy lighthouse to halyard", target: "halyard", undo: "redeploy the previous build", state: "waiting" } satisfies ProofInit,
  ],
};
const rotaGet: EpicsGetResponse = create(EpicsGetResponseSchema, rotaGetInit);

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
    id: "orrery/orrery-followups", repository: "orrery", path: "/home/owner/code/orrery", slug: "orrery-followups", steering: "sess-follow-22", outcome: "accepted",
    changed: at("@14:40"), live: true, runningSteps: [followupsT1Impl, followupsT2Review], endedSteps: 1,
    source: "orreryd", phaseAt: at("@14:21"), seen: 0n,
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
      id: "T1", status: "open", stage: "implement", requirements: ["R1"], place: "implement", tone: "run",
      title: "The done-judge sees a long Task or Return section’s head and tail, with a line naming the bytes left out.",
    },
    {
      id: "T2", status: "open", stage: "review", requirements: ["R2"], place: "review", tone: "run",
      title: "A planner turn freed by a limit with no reset says “reset time unknown”.",
    },
    {
      id: "T3", status: "open", stage: "", requirements: ["R3"], place: "planned", tone: "plan",
      title: "The docs state the new judge input and the new limit text.",
    },
  ],
  steps: [followupsT1Impl, followupsT2Impl, followupsT2Review],
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
    id: "orrery/holder-scopes", repository: "orrery", path: "/home/owner/code/orrery", slug: "holder-scopes", steering: "sess-holder-19", outcome: "released",
    changed: at("@09:52"), live: false, runningSteps: [], endedSteps: 4, source: "orreryd",
    phaseAt: at("@09:52"), seen: at("@09:55"),
  },
  repository: "/home/owner/Personal/orrery",
  summary:
    "Each holder runs in its own systemd scope, so a runaway one is killed alone and the terminal memory cap covers it. " +
    "orreryd starts as Type=notify with a 900 s ceiling, and the devspaces doc no longer names an orrery version.",
  sessions: [{ id: "sess-holdplan", pane: "", label: "Planned holder scopes", call: "epic_create", at: at("@08:04") }],
  reviews: [{ position: 1n, reviewer: "final review", step: "st-holder-final-review", verdict: "holds", reason: "0 findings." }],
  report: {
    outcome: "released",
    shipped: ["R1", "R2", "R3", "R4"],
    notShipped: [],
    why: "All four requirements shipped. It ran 1 h 48 min, from acceptance at 08:04 to v0.45.0 running on halyard at 09:52.",
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
      { id: "P2", text: "systemd on halyard is 255 or newer.", ifWrong: "Scopes need the older property names." },
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
      { step: "halyard runs v0.45.0", target: "halyard", undo: "switch to the previous generation" },
    ],
    releaseThen: "halyard runs v0.45.0 and every holder is scoped.",
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
      id: "T1", title: "Each holder runs in its own systemd scope.", status: "done", stage: "verify", requirements: ["R1"], place: "integrated", tone: "ok",
      red: holderR1Red, green: holderR1Green,
      reviews: [{ attempt: 1n, gate: "review", step: "", verdict: "holds", findings: [] }],
    },
    {
      id: "T2", title: "The terminal memory cap covers holder scopes.", status: "done", stage: "verify", requirements: ["R2"], place: "integrated", tone: "ok",
      red: holderR2Red, green: holderR2Green,
      reviews: [{ attempt: 1n, gate: "review", step: "", verdict: "holds", findings: [] }],
    },
    {
      id: "T3", title: "orreryd starts as Type=notify with a 900 s ceiling.", status: "done", stage: "verify", requirements: ["R3"], place: "integrated", tone: "ok",
      red: holderR3Red, green: holderR3Green,
      reviews: [{ attempt: 1n, gate: "review", step: "", verdict: "holds", findings: [] }],
    },
    {
      id: "T4", title: "The devspaces doc names no orrery version.", status: "done", stage: "verify", requirements: ["R4"], place: "integrated", tone: "ok",
      green: holderR4Green,
      reviews: [{ attempt: 1n, gate: "review", step: "", verdict: "holds", findings: [] }],
    },
  ],
  steps: holderSteps,
  releaseSteps: [
    { position: 1n, step: holderReleaseStep1, target: "github, tag v0.45.0", undo: "none once someone has fetched the tag", state: "passed", evidence: "github · 09:44 · signature good", reason: "The signed tag v0.45.0 is on github.", attempt: 1n, at: at("@09:44") } satisfies ProofInit,
    { position: 2n, step: "CI green on the tag", target: "github actions", undo: "rerun", state: "passed", evidence: "run 5490 · 09:51", reason: "CI is green on the tag.", attempt: 1n, at: at("@09:51") } satisfies ProofInit,
    { position: 3n, step: "halyard runs v0.45.0", target: "halyard", undo: "switch to the previous generation", state: "passed", evidence: "orrery --version: orrery 0.45.0", reason: "halyard runs v0.45.0.", attempt: 1n, at: at("@09:52") } satisfies ProofInit,
  ],
});

const holderHistory: HistoryInit[] = [
  { time: at("@08:04"), cursor: "c1", item: { case: "tell", value: { seq: 1n, text: "You accepted the contract: 4 requirements, serious care.", at: at("@08:04"), told: true } } },
  { time: at("@09:31"), cursor: "c2", item: { case: "tell", value: { seq: 2n, text: "docs-devspaces integrated: the last task.", at: at("@09:31"), told: true } } },
  { time: at("@09:38"), cursor: "c3", item: { case: "tell", value: { seq: 3n, text: "Final review of the epic branch: 0 findings.", at: at("@09:38"), told: true } } },
  { time: at("@09:39"), cursor: "c4", item: { case: "tell", value: { seq: 4n, text: "orrery started the release by itself, because the epic is clean.", at: at("@09:39"), told: true } } },
  { time: at("@09:44"), cursor: "c5", item: { case: "tell", value: { seq: 5n, text: "Tagged v0.45.0 and pushed. CI went green on the tag.", at: at("@09:44"), told: true } } },
  { time: at("@09:52"), cursor: "c6", item: { case: "tell", value: { seq: 6n, text: "Released v0.45.0. halyard runs it.", at: at("@09:52"), told: true } } },
];

// ---------------------------------------------------------------------------------------------------------------------
// tidepool/tide-alerts
// ---------------------------------------------------------------------------------------------------------------------

const nbsp = " ";

const tideGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: {
    id: "tidepool/tide-alerts", repository: "tidepool", path: "/home/owner/code/tidepool", slug: "tide-alerts", steering: "sess-tide-16", outcome: "contract written",
    changed: at("@14:15"), live: true, runningSteps: [], endedSteps: 0, source: "orreryd",
    phaseAt: at("@14:15"), seen: 0n,
  },
  repository: "/home/owner/code/tidepool",
  summary:
    `A text the evening before any tide forecast above 4.5${nbsp}m at the harbour, so the boat can be moved before the ` +
    "slipway floods. The number comes from config only, each tide sends one alert, and last year’s tables replay with " +
    "none missed. A dry-run flag logs instead of sending, if you keep it.",
  sessions: [{ id: "sess-tideplan", pane: "", label: "Wrote the tide-alerts contract", call: "epic_create", at: at("@14:15") }],
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
    approach: "A timer on halyard reads tomorrow’s forecast every evening at 18:00 and sends one text per tide above 4.5 m through the SMS gateway, keyed by tide id.",
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
      { step: "Deploy the alert job to halyard", target: "halyard", undo: "disable the timer" },
    ],
    releaseThen: "Then halyard texts you the evening before each tide above 4.5 m.",
    checks: "just check on every return; a replay of last year’s tables as the evidence for R2.",
    base: "",
    accepted: 0n,
  },
  releaseSteps: [
    { position: 1n, step: "Send texts through the SMS gateway", target: "SMS gateway, the number in config", undo: "none, a sent text stays sent", state: "waiting" } satisfies ProofInit,
    { position: 2n, step: "Deploy the alert job to halyard", target: "halyard", undo: "disable the timer", state: "waiting" } satisfies ProofInit,
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
    id: "tidepool/offline-tables", repository: "tidepool", path: "/home/owner/code/tidepool", slug: "offline-tables", steering: "sess-offline-17", outcome: "stopped",
    changed: at("@14:32"), live: true, runningSteps: [], endedSteps: 0, source: "orreryd",
    phaseAt: at("@14:32"), seen: 0n,
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
    { id: "T1", title: "Tide tables for the home port are stored on the phone.", status: "done", stage: "", requirements: ["R1", "R2"], place: "integrated", tone: "ok" },
    { id: "T2", title: "The sync worker retries with backoff and never blocks the screen.", status: "done", stage: "", requirements: ["R3", "R4"], place: "integrated", tone: "ok" },
    { id: "T3", title: "The tables screen works with no network and says how old its data is.", status: "done", stage: "", requirements: ["R5", "R6"], place: "integrated", tone: "ok" },
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
    id: "orrery/orbit-labels", repository: "orrery", path: "/home/owner/code/orrery", slug: "orbit-labels", steering: "sess-follow-22", outcome: "releasing",
    changed: at("@14:40"), live: true, runningSteps: [], endedSteps: 0, source: "orreryd",
    phaseAt: at("@14:37"), seen: 0n,
  },
  repository: "/home/owner/Personal/orrery",
  requirements: [
    { id: "R1", kind: "check", text: "Every orbit on the epic chart carries its stage name.", runs: [greenRun("ev-orbit-r1-green", false)] },
    { id: "R2", kind: "check", text: "Hovering a body shows its task, state and timing.", runs: [greenRun("ev-orbit-r2-green", false)] },
    { id: "R3", kind: "check", text: "The phone draws the same chart from the same cells.", runs: [greenRun("ev-orbit-r3-green", false)] },
    { id: "R4", kind: "review", text: "The chart spec states the new labels.", runs: [greenRun("ev-orbit-r4-green", true)] },
  ],
  buildTasks: [
    { id: "T1", title: "Every orbit carries its stage name.", status: "done", stage: "", requirements: ["R1"], place: "integrated", tone: "ok" },
    { id: "T2", title: "A card for every body, on the web and the phone.", status: "done", stage: "", requirements: ["R2", "R3"], place: "integrated", tone: "ok" },
    { id: "T3", title: "The chart spec states the new labels.", status: "done", stage: "", requirements: ["R4"], place: "integrated", tone: "ok" },
  ],
  releaseSteps: [
    // The target and undo of this step are left open, so both are empty.
    { position: 1n, step: "Merge the epic branch to main", target: "", undo: "", state: "passed", at: at("@14:38") } satisfies ProofInit,
    { position: 2n, step: "Push tag v0.46.0, signed", target: "github, tag v0.46.0", undo: "none once someone has fetched the tag", state: "running" } satisfies ProofInit,
    { position: 3n, step: "CI green on the tag", target: "github actions", undo: "rerun", state: "waiting" } satisfies ProofInit,
  ],
});

// ---------------------------------------------------------------------------------------------------------------------
// orrery/notices-usable
// ---------------------------------------------------------------------------------------------------------------------

// noticesR5Red is R5's red run as the engine ran it at the epic's base: the check passed there, so the requirement is
// already met and its red is not proven.
const noticesR5Red = run("ev-notices-r5-red", "", { phase: "red", exit: 0, verdict: "holds", passed: false, created: at("@-1d 09:41") });

const noticesGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: {
    id: "orrery/notices-usable", repository: "orrery", path: "/home/owner/code/orrery", slug: "notices-usable", steering: "sess-follow-22", outcome: "accepted",
    changed: at("@-1d 09:41"), live: true, runningSteps: [], endedSteps: 0, source: "orreryd",
    phaseAt: at("@-1d 09:41"), seen: 0n,
  },
  repository: "/home/owner/Personal/orrery",
  summary:
    "Notices name a directory and what its session is doing, never an id; they stack three deep and count the rest; a click " +
    "goes to the thing the notice is about.",
  parked: "R5 is already met at the epic’s base",
  questions: [{
    id: "Q1",
    text: "Requirement R5 is already met before any change. Its red check ran at the epic’s base and passed, so there is nothing to prove red. Run the check again with a fresh judge, drop R5 as already met, or end the epic.",
    turn: 2n, asked: at("@-1d 09:41"),
    choices: [
      { id: "retry", does: "Run R5’s red check again with a fresh judge." },
      { id: "drop", does: "Record R5 as already met, as your decision, and go on without its red." },
      { id: "end", does: "Stop here. The epic ends as “already met”." },
    ],
    recommendation: { choice: "retry", why: "The first judge may have read stale output.", confidence: "medium" },
  } satisfies QuestionInit],
  contract: {
    title: "notices-usable",
    approved: "Approved: notices name a directory and what its session is doing, and stack three deep.",
    intent: "A notice should say what it is about, in words, so the owner can act on it without an id.",
    care: "limited",
    approach: "Name the directory and the session’s state in each notice, stack three deep and count the rest, and open the thing a click names.",
    checks: "just check on every return.",
    accepted: at("@-1d 09:12"),
  },
  requirements: [
    { id: "R1", kind: "check", text: "A notice names its directory and what its session is doing, never an id.", evidence: "go test ./internal/notices/ -run TestNamesNeverIDs", conclusion: "", runs: [] },
    { id: "R2", kind: "check", text: "Notices stack three deep and count the rest.", evidence: "go test ./internal/notices/ -run TestStackThreeDeep", conclusion: "", runs: [] },
    { id: "R3", kind: "check", text: "A click on a notice goes to the thing it is about.", evidence: "go test ./internal/notices/ -run TestClickOpensSubject", conclusion: "", runs: [] },
    { id: "R4", kind: "check", text: "A notice about an epic draws the epic’s gauge.", evidence: "go test ./internal/notices/ -run TestEpicNoticeDrawsGauge", conclusion: "", runs: [] },
    { id: "R5", kind: "check", text: "Each notice says when it happened.", evidence: "go test ./internal/notices/ -run TestNoticeWhen", conclusion: "", runs: [noticesR5Red] },
  ],
});

// ---------------------------------------------------------------------------------------------------------------------
// orrery/harbour-map
// ---------------------------------------------------------------------------------------------------------------------

const harbourR1Green = greenRun("ev-harbour-r1-green", false);
const harbourR2Green = greenRun("ev-harbour-r2-green", false);

const harbourTask = (
  id: string,
  title: string,
  status: string,
  stage: string,
  requirements: string[],
  place: string,
  tone: string,
): TaskInit => ({ id, title, status, stage, requirements, place, tone });

const harbourGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: {
    id: "orrery/harbour-map", repository: "orrery", path: "/home/owner/code/orrery", slug: "harbour-map", steering: "sess-follow-22", outcome: "accepted",
    changed: at("@11:41"), live: true, runningSteps: [], endedSteps: 0, source: "orreryd",
    phaseAt: at("@11:41"), seen: 0n,
  },
  repository: "/home/owner/Personal/orrery",
  paused: true,
  contract: {
    title: "harbour-map",
    approved: "Approved: a map of the harbour that draws each berth with its depth at the tide.",
    intent: "A map of the harbour the owner can read from one page.",
    care: "limited",
    approach: "Draw each berth with its depth from the day’s tide table, and read the table when the page opens.",
    checks: "just check on every return.",
    accepted: at("@-1d 16:20"),
  },
  requirements: [
    { id: "R1", kind: "check", text: "Each berth is drawn with its depth at the tide.", evidence: "go test ./internal/harbour/ -run TestBerthDepth", conclusion: "", runs: [harbourR1Green] },
    { id: "R2", kind: "check", text: "The depth follows the tide table for the day.", evidence: "go test ./internal/harbour/ -run TestTideTableDay", conclusion: "", runs: [harbourR2Green] },
    { id: "R3", kind: "check", text: "The map pans and zooms.", evidence: "go test ./internal/harbour/ -run TestPanZoom", conclusion: "", runs: [] },
    { id: "R4", kind: "check", text: "The map keeps working with no network.", evidence: "go test ./internal/harbour/ -run TestNoNetwork", conclusion: "", runs: [] },
  ],
  buildTasks: [
    harbourTask("T1", "Each berth is drawn with its depth", "done", "", ["R1"], "integrated", "ok"),
    harbourTask("T2", "A berth’s depth is read at the tide", "done", "", ["R1"], "integrated", "ok"),
    harbourTask("T3", "The day’s tide table is read on open", "done", "", ["R2"], "integrated", "ok"),
    harbourTask("T4", "The depth follows the day’s table", "done", "", ["R2"], "integrated", "ok"),
    harbourTask("T5", "A stale table is marked on the map", "done", "", ["R2"], "integrated", "ok"),
    harbourTask("T6", "The map pans with a drag", "open", "implement", ["R3"], "implement", "mine"),
    harbourTask("T7", "The map zooms with the wheel", "open", "", ["R3"], "planned", "plan"),
    harbourTask("T8", "The map keeps its last table with no network", "open", "", ["R4"], "planned", "plan"),
    harbourTask("T9", "The map says how old its table is with no network", "open", "", ["R4"], "planned", "plan"),
  ],
});

// ---------------------------------------------------------------------------------------------------------------------
// tidepool/buoy-feeds
// ---------------------------------------------------------------------------------------------------------------------

const buoyGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: {
    id: "tidepool/buoy-feeds", repository: "tidepool", path: "/home/owner/code/tidepool", slug: "buoy-feeds", steering: "6c2f9e14-3a7b-4d58-9b0e-2f7a1c8d4e55", outcome: "accepted",
    changed: at("@14:38"), live: true, runningSteps: [], endedSteps: 0, source: "orreryd",
    phaseAt: at("@14:38"), seen: 0n,
  },
  repository: "/home/owner/code/tidepool",
  questions: [{
    id: "Q1",
    text: "Flag a gap at five minutes for every buoy, or at three for the storm buoys?",
    turn: 2n, asked: at("@14:39"),
  } satisfies QuestionInit],
  summary:
    "Each buoy’s feed is read once a minute, gaps over five minutes are flagged, and the harbour page shows the newest " +
    "reading with its age.",
  contract: {
    title: "buoy-feeds",
    approved: "Approved: each buoy’s feed read once a minute, with gaps over five minutes flagged on the harbour page.",
    intent: "The harbour page should show each buoy’s newest reading and how old it is.",
    care: "limited",
    approach: "Read each buoy’s feed once a minute, flag a gap over five minutes, and show the newest reading with its age.",
    checks: "just check on every return.",
    accepted: at("@14:38"),
  },
  requirements: [
    { id: "R1", kind: "check", text: "Each buoy’s feed is read once a minute.", evidence: "go test ./internal/feeds/ -run TestReadOnceAMinute", conclusion: "", runs: [] },
    { id: "R2", kind: "check", text: "A gap over five minutes in a feed is flagged.", evidence: "go test ./internal/feeds/ -run TestFlagsGap", conclusion: "", runs: [] },
    { id: "R3", kind: "check", text: "The harbour page shows each buoy’s newest reading.", evidence: "go test ./internal/feeds/ -run TestNewestReading", conclusion: "", runs: [] },
    { id: "R4", kind: "check", text: "The harbour page shows the age of each reading.", evidence: "go test ./internal/feeds/ -run TestReadingAge", conclusion: "", runs: [] },
  ],
});

// ---------------------------------------------------------------------------------------------------------------------
// tidepool/tide-cache
// ---------------------------------------------------------------------------------------------------------------------

const tidecacheEpic: EpicRow = {
  id: "tidepool/tide-cache", repository: "tidepool", path: "/home/owner/code/tidepool", slug: "tide-cache", steering: "sess-cache-20", outcome: "closed_superseded",
  changed: at("@-4d 09:12"), live: false, runningSteps: [], endedSteps: 2, source: "orreryd",
  phaseAt: at("@-4d 09:12"), seen: at("@-4d 09:30"),
};
const tidecacheGetInit: Plain<MessageInitShape<typeof EpicsGetResponseSchema>> = {
  epic: tidecacheEpic,
  repository: "/home/owner/code/tidepool",
  requirements: [
    { id: "R1", kind: "check", text: "Tide tables are cached on disk.", runs: [] },
    { id: "R2", kind: "check", text: "The cache drops tables older than a week.", runs: [] },
    { id: "R3", kind: "review", text: "The docs describe the cache.", runs: [] },
  ],
  // This epic has no task titles, so each task carries its requirement's text.
  buildTasks: [
    { id: "T1", title: "Tide tables are cached on disk.", status: "open", stage: "", requirements: ["R1"], place: "planned", tone: "plan" },
    { id: "T2", title: "The cache drops tables older than a week.", status: "open", stage: "", requirements: ["R2"], place: "planned", tone: "plan" },
    { id: "T3", title: "The docs describe the cache.", status: "open", stage: "", requirements: ["R3"], place: "planned", tone: "plan" },
  ],
  report: {
    outcome: "closed_superseded",
    shipped: [],
    notShipped: ["R1", "R2", "R3"],
    why: "You ended it, superseded by offline-tables, which covers the same ground. Two unit branches are kept, unmerged.",
  },
};
const tidecacheGet: EpicsGetResponse = create(EpicsGetResponseSchema, tidecacheGetInit);

// ---------------------------------------------------------------------------------------------------------------------
// almanac/moon-rounding: archived, under its archived conversation Moon phase rounding
// ---------------------------------------------------------------------------------------------------------------------

const moonRoundingEpic: EpicInit = {
  id: "almanac/moon-rounding", repository: "almanac", path: "/home/owner/code/almanac", slug: "moon-rounding", steering: "e7c4b2a1-5d9f-4e3a-9b61-2a8f0c4d7e13", outcome: "released",
  changed: at("@-9d 14:00"), live: false, runningSteps: [], endedSteps: 0, source: "orreryd",
  phaseAt: at("@-9d 14:00"), seen: at("@-9d 14:30"), archived: true,
};

const moonRoundingGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: moonRoundingEpic,
  repository: "/home/owner/code/almanac",
  requirements: [
    { id: "R1", kind: "check", text: "The moon phase rounds at the date line the way the almanac says it does.", evidence: "go test ./internal/moon/ -run TestRoundingAtDateLine", conclusion: "", runs: [] },
  ],
  report: {
    outcome: "released",
    shipped: ["R1"],
    notShipped: [],
    why: "Fixed and merged.",
  },
});

// ---------------------------------------------------------------------------------------------------------------------
// tidepool/tide-warmup: archived, under the stopped conversation Tide table cache warm-up, which stays listed
// ---------------------------------------------------------------------------------------------------------------------

const tideWarmupEpic: EpicInit = {
  id: "tidepool/tide-warmup", repository: "tidepool", path: "/home/owner/code/tidepool", slug: "tide-warmup", steering: "9b5e1a47-2c0d-4e8f-8a31-6d7c0f5b2e90", outcome: "stopped",
  changed: at("@-2d 14:41"), live: false, runningSteps: [], endedSteps: 0, source: "orreryd",
  phaseAt: at("@-2d 14:41"), seen: at("@-2d 14:42"), archived: true,
};

const tideWarmupGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: tideWarmupEpic,
  repository: "/home/owner/code/tidepool",
  requirements: [
    { id: "R1", kind: "check", text: "Every port’s tide table is cached for the next thirty days.", evidence: "just warm", conclusion: "", runs: [] },
  ],
  report: {
    outcome: "stopped",
    shipped: [],
    notShipped: ["R1"],
    why: "You stopped it.",
  },
});

// The Epics.List rows, in order.
const epicRows: EpicInit[] = [
  {
    id: "orrery/orrery-followups", repository: "orrery", path: "/home/owner/code/orrery", slug: "orrery-followups", steering: "sess-follow-22", outcome: "accepted", changed: at("@14:40"),
    live: true, runningSteps: [followupsT1Impl, followupsT2Review], endedSteps: 1, source: "orreryd",
    phaseAt: at("@14:21"), seen: 0n,
  },
  {
    id: "lighthouse/rota-export", repository: "lighthouse", path: "/home/owner/code/lighthouse", slug: "rota-export", steering: "sess-rota-14", outcome: "accepted", changed: at("@14:40"),
    live: true, runningSteps: [rotaT2Impl, rotaT1Impl], endedSteps: 5, source: "orreryd",
    phaseAt: at("@13:52"), seen: 0n,
  },
  {
    id: "tidepool/tide-alerts", repository: "tidepool", path: "/home/owner/code/tidepool", slug: "tide-alerts", steering: "sess-tide-16", outcome: "contract written",
    changed: at("@14:15"), live: true, runningSteps: [], endedSteps: 0, source: "orreryd",
    phaseAt: at("@14:15"), seen: 0n,
  },
  {
    id: "tidepool/offline-tables", repository: "tidepool", path: "/home/owner/code/tidepool", slug: "offline-tables", steering: "sess-offline-17", outcome: "stopped",
    changed: at("@14:32"), live: true, runningSteps: [], endedSteps: 0, source: "orreryd",
    phaseAt: at("@14:32"), seen: 0n,
  },
  {
    id: "orrery/orbit-labels", repository: "orrery", path: "/home/owner/code/orrery", slug: "orbit-labels", steering: "sess-follow-22", outcome: "releasing", changed: at("@14:40"),
    live: true, runningSteps: [], endedSteps: 0, source: "orreryd",
    phaseAt: at("@14:37"), seen: 0n,
  },
  {
    id: "orrery/notices-usable", repository: "orrery", path: "/home/owner/code/orrery", slug: "notices-usable", steering: "sess-follow-22", outcome: "accepted", changed: at("@-1d 09:41"),
    live: true, runningSteps: [], endedSteps: 0, source: "orreryd",
    phaseAt: at("@-1d 09:41"), seen: 0n, parked: "R5 is already met at the epic’s base",
  },
  {
    id: "orrery/harbour-map", repository: "orrery", path: "/home/owner/code/orrery", slug: "harbour-map", steering: "sess-follow-22", outcome: "accepted", changed: at("@11:41"),
    live: true, runningSteps: [], endedSteps: 0, source: "orreryd",
    phaseAt: at("@11:41"), seen: 0n, paused: true, pauseSource: "owner",
  },
  {
    id: "tidepool/buoy-feeds", repository: "tidepool", path: "/home/owner/code/tidepool", slug: "buoy-feeds", steering: "6c2f9e14-3a7b-4d58-9b0e-2f7a1c8d4e55", outcome: "accepted", changed: at("@14:38"),
    live: true, runningSteps: [], endedSteps: 0, source: "orreryd",
    phaseAt: at("@14:38"), seen: 0n,
  },
  {
    id: "orrery/holder-scopes", repository: "orrery", path: "/home/owner/code/orrery", slug: "holder-scopes", steering: "sess-holder-19", outcome: "released", changed: at("@09:52"),
    live: false, runningSteps: [], endedSteps: 4, source: "orreryd",
    phaseAt: at("@09:52"), seen: at("@09:55"),
  },
  {
    id: "tidepool/tide-cache", repository: "tidepool", path: "/home/owner/code/tidepool", slug: "tide-cache", steering: "sess-cache-20", outcome: "closed_superseded",
    changed: at("@-4d 09:12"), live: false, runningSteps: [], endedSteps: 2, source: "orreryd",
    phaseAt: at("@-4d 09:12"), seen: at("@-4d 09:30"),
  },
  moonRoundingEpic,
  tideWarmupEpic,
];

// epics is the Epics.List answer: the epic rows, in the order the list draws them.
export const epics = create(EpicsListResponseSchema, { epics: epicRows });

// epicsListWith is the Epics.List answer of the shipped rows, each taken through change, with the added rows after them.
export function epicsListWith(change: (row: EpicInit) => EpicInit, added: EpicInit[] = []): EpicsListResponse {
  return create(EpicsListResponseSchema, { epics: [...epicRows.map(change), ...added] });
}

// gets holds the Epics.Get answer of each epic that has one, by epic id.
export const gets = new Map<string, EpicsGetResponse>([
  ["orrery/orrery-followups", followupsGet],
  ["lighthouse/rota-export", rotaGet],
  ["tidepool/tide-alerts", tideGet],
  ["tidepool/offline-tables", offlineGet],
  ["orrery/orbit-labels", orbitGet],
  ["orrery/notices-usable", noticesGet],
  ["orrery/harbour-map", harbourGet],
  ["tidepool/buoy-feeds", buoyGet],
  ["orrery/holder-scopes", holderGet],
  ["tidepool/tide-cache", tidecacheGet],
  ["almanac/moon-rounding", moonRoundingGet],
  ["tidepool/tide-warmup", tideWarmupGet],
]);

// tellHistory is a history of told lines, oldest first, each at its time.
function tellHistory(lines: [time: bigint, text: string][]): HistoryInit[] {
  return lines.map(([time, text], index) => ({
    time, cursor: `t${index + 1}`, item: { case: "tell", value: { seq: BigInt(index + 1), text, at: time, told: true } },
  }));
}

// noticesHistory is notices-usable's history the parked page scenario draws, and harbourHistory is harbour-map's history
// the paused page scenario draws. Both are round 6's lines.
const noticesHistory = tellHistory([
  [at("@-1d 09:41"), "red run of R5 · passed, judged holds"],
  [at("@-1d 09:41"), "The engine parked the epic: R5 met at the epic’s base"],
]);
const harbourHistory = tellHistory([
  [at("@14:31"), "csv-writer · done · accepted by the judge"],
  [at("@14:38"), "export-cli · checks passed, the gate is running"],
]);

// histories holds the Epics.History answer of each epic that has one, by epic id.
export const histories = new Map<string, EpicsHistoryResponse>([
  ["lighthouse/rota-export", create(EpicsHistoryResponseSchema, { entries: rotaHistory, next: "" })],
  ["orrery/holder-scopes", create(EpicsHistoryResponseSchema, { entries: holderHistory, next: "" })],
  ["orrery/notices-usable", create(EpicsHistoryResponseSchema, { entries: noticesHistory, next: "" })],
  ["orrery/harbour-map", create(EpicsHistoryResponseSchema, { entries: harbourHistory, next: "" })],
]);

// ---------------------------------------------------------------------------------------------------------------------
// The epic states the page scenarios draw. Each changes the shipped answer of rota-export or buoy-feeds in the one way its
// scenario names, or adds deck-pipeline. slices.ts draws each scenario over the shipped slices with these.
// ---------------------------------------------------------------------------------------------------------------------

// rotaStateEpic is rota-export's Epics.List row in a state: the shipped row, with the steps that have not ended as its
// running steps, and the fields the state changes.
function rotaStateEpic(steps: StepInit[], change: Partial<Plain<EpicInit>> = {}): Plain<EpicInit> {
  return { ...rotaEpic, runningSteps: steps.filter((step) => step.end === ""), ...change };
}

// rotaStateGet is rota-export's Epics.Get answer in a state: its steps, the epic row, the detail fields the state
// changes, and the tone the server gives each build task tones names, by task id. No state here asks a question, so
// none is given.
function rotaStateGet(
  steps: StepInit[],
  tones: Record<string, string>,
  change: Partial<Plain<EpicInit>> = {},
  detail: { parked?: string; plannerParked?: boolean; requirements?: RequirementInit[] } = {},
): EpicsGetResponse {
  const buildTasks = rotaTasks.map((task) => ({ ...task, tone: tones[task.id ?? ""] ?? task.tone }));
  return create(EpicsGetResponseSchema, {
    ...rotaGetInit, ...detail, epic: rotaStateEpic(steps, change), steps, buildTasks, questions: [],
  });
}

// rotaRunningTones and rotaLimitedTones are the tones of the building states: tz-fixtures' red runs, held by nothing,
// and in the limited state it and export-cli wait on the weekly limit.
const rotaRunningTones = { T1: "run" };
const rotaLimitedTones = { T1: "limited", T2: "limited" };


// rotaRedRunning is tz-fixtures' step while its red stage runs. Nothing holds it, so no question asks about it.
const rotaRedRunning: StepInit = {
  ...rotaT1Impl, end: "", status: "running", held: false, stage: "turn", stageStarted: at("@14:39"),
  doing: "Capturing the failing test for the Europe/Lisbon rule.", reason: "",
};
// rotaLimitReset is when the weekly usage limit the limited steps wait on resets.
const rotaLimitReset = at("@15:10");
const rotaT2Limited: StepInit = { ...rotaT2Impl, limited: true, limitKind: "weekly", limitResets: rotaLimitReset };
const rotaRedLimited: StepInit = { ...rotaRedRunning, limited: true, limitKind: "weekly", limitResets: rotaLimitReset };

const rotaBuildingSteps: StepInit[] = [rotaT5Try1, rotaT4Impl, rotaT5Try2, rotaT2Impl, rotaT3Impl, rotaRedRunning, rotaT3Review];
const rotaLimitedSteps: StepInit[] = [rotaT5Try1, rotaT4Impl, rotaT5Try2, rotaT2Limited, rotaT3Impl, rotaRedLimited, rotaT3Review];

// The history of the building states: the shipped entries, without the question tz-fixtures asked and the step it held.
const rotaBuildingHistory = rotaHistory.filter((entry) => entry.cursor !== "c8" && entry.cursor !== "c11");

// buoyPlanningHistory is round 6's planning history for buoy-feeds: the epic created from its contract, then the planner's
// first turn running. denseHistory is the building history the dense epic carries, as round 6's dense state inherits it.
export const buoyPlanningHistory = create(EpicsHistoryResponseSchema, {
  entries: tellHistory([
    [at("@14:38"), "The epic was created from the accepted contract"],
    [at("@14:38") + 1n, "planner turn 1 · running"],
  ]),
  next: "",
});
export const denseHistory = create(EpicsHistoryResponseSchema, { entries: rotaBuildingHistory, next: "" });

// rotaPlannerHistory is the building history with the planner's taking of the park, as the newest entry.
const rotaPlannerHistory: HistoryInit[] = [
  ...rotaBuildingHistory,
  {
    time: at("@14:44"), cursor: "c13",
    item: { case: "tell", value: { seq: 5n, text: "The planner took the park: gate failed on export-cli.", at: at("@14:44"), told: true } },
  },
];

// The rota-export states: the list row and the Epics.Get answer each reads, and the history it shows.
export const rotaBuilding = {
  row: rotaStateEpic(rotaBuildingSteps),
  get: rotaStateGet(rotaBuildingSteps, rotaRunningTones),
  history: create(EpicsHistoryResponseSchema, { entries: rotaBuildingHistory, next: "" }),
};
export const rotaLimited = {
  row: rotaStateEpic(rotaLimitedSteps),
  get: rotaStateGet(rotaLimitedSteps, rotaLimitedTones),
  history: create(EpicsHistoryResponseSchema, { entries: rotaBuildingHistory, next: "" }),
};
export const rotaPlanner = {
  row: rotaStateEpic(rotaBuildingSteps),
  get: rotaStateGet(rotaBuildingSteps, rotaRunningTones, {}, { parked: "workflow node gate failed", plannerParked: true }),
  history: create(EpicsHistoryResponseSchema, { entries: rotaPlannerHistory, next: "" }),
};

// rotaEvidenceRequirements are rota-export's requirements with R4's green run judging, as the evidence scenario draws R4.
const rotaR4Judged: RequirementInit = { ...rotaR4Requirement, runs: [rotaR4Red, rotaR4Judging] };
const rotaEvidenceRequirements: RequirementInit[] = rotaRequirements.map((requirement) =>
  requirement.id === "R4" ? rotaR4Judged : requirement,
);
export const rotaEvidence = {
  row: rotaStateEpic(rotaBuildingSteps),
  get: rotaStateGet(rotaBuildingSteps, rotaRunningTones, {}, { requirements: rotaEvidenceRequirements }),
  history: create(EpicsHistoryResponseSchema, { entries: rotaBuildingHistory, next: "" }),
};

// rotaReviewingSteps are rota-export's steps once every build task is done: each one ended, and tz-fixtures' held step and
// its red left out.
const rotaT2Done: StepInit = { ...rotaT2Impl, end: "done", status: "ended", stage: "", doing: "", ended: at("@14:36") };
const rotaReviewingSteps: StepInit[] = [rotaT5Try1, rotaT4Impl, rotaT5Try2, rotaT2Done, rotaT3Impl, rotaT3Review];

// rotaReviewingGet is rota-export in its review: every build task done, the contract reviewer holding and the security
// reviewer failing, and its one finding confirmed with a closure still open.
const rotaReviewingGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  ...rotaGetInit,
  epic: rotaStateEpic(rotaReviewingSteps, { outcome: "reviewing" }),
  steps: rotaReviewingSteps,
  questions: [],
  buildTasks: rotaTasks.map((task) => ({ ...task, status: "done", stage: "verify", place: "integrated", tone: "ok" })),
  reviews: [
    { position: 1n, reviewer: "contract", step: "", verdict: "holds", reason: "Every requirement has a red and a green run at the stored base." },
    { position: 2n, reviewer: "security", step: "", verdict: "fails", reason: "One finding: export writes outside the rota directory when the name holds “../”." },
  ],
  ledger: [{
    id: "security-1", position: 1n, reviewer: "security",
    claim: "Export writes outside the rota directory when the name holds “../”.",
    location: "export.go:41", step: "", result: "confirmed", unverified: false,
    reason: "Ran the export with a name that holds ../, and the file was written outside the rota directory.",
    closures: [{ round: 1n, step: "", result: "open", reason: "a fix plan will close it" }],
  }],
});
export const rotaReviewing = {
  row: rotaStateEpic(rotaReviewingSteps, { outcome: "reviewing" }),
  get: rotaReviewingGet,
  history: create(EpicsHistoryResponseSchema, { entries: rotaBuildingHistory, next: "" }),
};

// orbitReleasing is orbit-labels releasing: its release table is orbitGet's, step 2 running, and its contract's Then line
// is drawn under it.
export const orbitReleasing: EpicsGetResponse = {
  ...orbitGet,
  contract: create(EpicContractSchema, {
    title: "orbit-labels",
    releaseThen: "the judge reads the tag and the notes, and the epic ends released.",
  }),
};

// offlineStopped is offline-tables stopped at step 2, whose undo is none, so Release from step 2 is offered.
// Its summary and contract draw the page's lede with its Contract and Steered from doors, as round 6's scenario does.
export const offlineStopped: EpicsGetResponse = {
  ...offlineGet,
  summary: "Tide tables are readable with no network: the last seven days are kept on the phone and marked with their age.",
  contract: create(EpicContractSchema, { title: "offline-tables", care: "serious", accepted: at("@11:20") }),
  releaseSteps: offlineGet.releaseSteps.map((step) => (step.position === 2n ? { ...step, undo: "none" } : step)),
};

// offlineStoppedHistory is what just happened to offline-tables: its release ran step 1 and stopped at step 2.
export const offlineStoppedHistory = create(EpicsHistoryResponseSchema, {
  entries: [
    { time: at("@14:05"), cursor: "c1", item: { case: "tell", value: { seq: 1n, text: "Release step 1 passed: v0.4.0 is tagged and pushed.", at: at("@14:05"), told: true } } },
    { time: at("@14:32"), cursor: "c2", item: { case: "tell", value: { seq: 2n, text: "Release step 2 failed: CI is red on the tag. It has no undo, so it waits on you.", at: at("@14:32"), told: true } } },
  ],
  next: "",
});

// tidecacheGaps is tide-cache ended with gaps: its report ships none and names R1, R2 and R3 as not shipped, with the why
// its gap was given. The Report draws the two owner calls, Release it anyway and Close as….
// tidecacheGapsRow is tide-cache's list row ended with gaps.
export const tidecacheGapsRow: EpicRow = { ...tidecacheEpic, outcome: "ended_with_gaps" };
// Every task of it is done and R1 and R2 are proven, as round 6's scenario draws them; its summary and contract draw the
// page's lede and doors.
export const tidecacheGaps: EpicsGetResponse = create(EpicsGetResponseSchema, {
  ...tidecacheGetInit,
  epic: tidecacheGapsRow,
  summary: "Tide tables are cached per port and day, and a stale table is never served past midnight.",
  contract: { title: "tide-cache", care: "limited", accepted: at("@-4d 10:11") },
  requirements: [
    { id: "R1", kind: "check", text: "Tide tables are cached on disk.", runs: [greenRun("ev-cache-r1-green", false)] },
    { id: "R2", kind: "check", text: "The cache drops tables older than a week.", runs: [greenRun("ev-cache-r2-green", false)] },
    { id: "R3", kind: "review", text: "The docs describe the cache.", runs: [] },
  ],
  buildTasks: (tidecacheGetInit.buildTasks ?? []).map((task) => ({
    ...task, status: "done", place: "integrated", tone: "ok",
  })),
  report: {
    outcome: "ended_with_gaps",
    shipped: [],
    notShipped: ["R1", "R2", "R3"],
    why: "R3 (a stale table is never served past midnight) was green on the change, but the review confirmed a finding the fix plans did not close in three rounds: the cache serves yesterday’s table for up to a minute after midnight.",
  },
});

// tidecacheGapsHistory is what just happened to tide-cache: its review left a gap, so it ended without releasing.
export const tidecacheGapsHistory = create(EpicsHistoryResponseSchema, {
  entries: [
    { time: at("@-4d 09:02"), cursor: "c1", item: { case: "tell", value: { seq: 1n, text: "The review confirmed a finding the fix plans did not close in three rounds.", at: at("@-4d 09:02"), told: true } } },
    { time: at("@-4d 09:12"), cursor: "c2", item: { case: "tell", value: { seq: 2n, text: "Ended with 1 gap. It did not release.", at: at("@-4d 09:12"), told: true } } },
  ],
  next: "",
});

// noticesParked is notices-usable parked on the owner's park alone. The engine's question is left out, so the park draws
// its own two choices, Retry and Stop, and no question's choices.
export const noticesParked: EpicsGetResponse = { ...noticesGet, questions: [] };

// buoyPlanning is buoy-feeds before its planner has outlined a task: its contract is accepted and nothing asks.
export const buoyPlanning: EpicsGetResponse = { ...buoyGet, questions: [] };

// The deck-pipeline epic: 120 tasks, 96 of them integrated and one a gap. Its running steps and the gap's failed step
// are the only steps it has.
function denseNumber(n: number): string {
  return String(n).padStart(3, "0");
}
// denseStage is the stage of task n, in its place of the chart: review, green, implement and red, then planned.
function denseStage(n: number): string {
  if (n <= 96) {
    return "";
  }
  if (n <= 99) {
    return "verify";
  }
  if (n <= 104) {
    return "green";
  }
  if (n <= 107) {
    return "implement";
  }
  if (n <= 110) {
    return "red";
  }
  return "";
}
// denseLife is where the server stands task n: the first 96 integrated, then at its stage's ring, running, except
// unit-102, a gap at green, and unit-107, whose running step is held; past unit-110, planned.
function denseLife(n: number): { place: string; tone: string } {
  if (n <= 96) {
    return { place: "integrated", tone: "ok" };
  }
  const place = { verify: "review", green: "green", implement: "implement", red: "red" }[denseStage(n)];
  if (place === undefined) {
    return { place: "planned", tone: "plan" };
  }
  return { place, tone: n === 102 ? "bad" : n === 107 ? "mine" : "run" };
}
const denseTasks: TaskInit[] = Array.from({ length: 120 }, (_, index) => ({
  id: `U${denseNumber(index + 1)}`,
  title: `unit-${denseNumber(index + 1)}`,
  status: index < 96 ? "done" : index === 101 ? "gap" : "open",
  stage: denseStage(index + 1),
  requirements: [`R${(index % 6) + 1}`],
  ...denseLife(index + 1),
}));
const denseStep = (n: number, doing: string): StepInit => ({
  ...gateDefaults, id: `st-deck-u${denseNumber(n)}`, task: `U${denseNumber(n)}`, agent: "haiku", role: "implementer",
  readOnly: false, end: "", status: "running", held: false, started: at("@14:20"), ended: 0n, stage: "turn",
  stageStarted: at("@14:36"), doing, reason: "", summary: "", checks: [],
});
// denseFailed is unit-102's only step: it ended at a failed check, and the task ended a gap, so its wedge is drawn
// failed.
const denseFailed: StepInit = {
  ...denseStep(102, ""), end: "checks_failed", status: "ended", ended: at("@14:30"),
  reason: "The playthrough did not match the recorded run.",
};
// unit-107's step is held by the owner, so its wedge is drawn held.
const denseRunning: StepInit[] = [
  denseStep(105, "Adding the manifest entry for the deck."),
  { ...denseStep(107, "Waiting on the owner’s word before it goes on."), held: true },
  denseStep(108, "Capturing the failing playthrough test."),
];
// denseRuns are a requirement's runs: its red run, which fails at the base, and its green run, which passes or is still
// capturing.
function denseRuns(n: number, passing: boolean): RunInit[] {
  const red = run(`ev-deck-r${n}-red`, "", { phase: "red", exit: 1, verdict: "fails", passed: true });
  const green = passing
    ? run(`ev-deck-r${n}-green`, "", { phase: "green", exit: 0, verdict: "holds", passed: true })
    : run(`ev-deck-r${n}-green`, "", { phase: "green", state: "capturing", passed: false });
  return [red, green];
}
// denseSteering is the session that steers deck-pipeline: the commonframe pane the dense scenario lists as an orrery session.
export const denseSteering = "sess-deck-21";
export const denseEpic: Plain<EpicInit> = {
  id: "commonframe/deck-pipeline", repository: "commonframe", path: "/home/owner/work/commonframe", slug: "deck-pipeline",
  steering: denseSteering, outcome: "accepted",
  changed: at("@14:40"), live: true, runningSteps: denseRunning, endedSteps: 1, source: "orreryd",
  phaseAt: at("@08:29"), seen: 0n,
};
export const denseGet: EpicsGetResponse = create(EpicsGetResponseSchema, {
  epic: denseEpic,
  repository: "/home/owner/work/commonframe",
  summary:
    "Every deck in the catalogue is rebuilt from its source, checked against its recorded playthrough, and published with a " +
    "manifest. 120 decks, six requirements.",
  contract: {
    title: "deck-pipeline",
    approved: "Approved: rebuild every deck from its source, and publish it only when it matches its playthrough.",
    intent: "Every deck in the catalogue should be published from its source, with a manifest of the whole catalogue.",
    care: "serious",
    approach: "Rebuild each deck, play it back against its recorded playthrough, and write the manifest from the results.",
    checks: "just check on every return.",
    accepted: at("@08:29"),
  },
  requirements: [
    { id: "R1", kind: "check", text: "Every deck is rebuilt from its source.", runs: denseRuns(1, true) },
    { id: "R2", kind: "check", text: "A rebuilt deck matches its recorded playthrough.", runs: denseRuns(2, true) },
    { id: "R3", kind: "check", text: "Every deck is published with a manifest.", runs: denseRuns(3, true) },
    { id: "R4", kind: "check", text: "The manifest lists every deck in the catalogue.", runs: denseRuns(4, true) },
    { id: "R5", kind: "check", text: "A deck that fails its playthrough is not published.", runs: denseRuns(5, false) },
    { id: "R6", kind: "check", text: "Every publish is written to the audit log.", runs: denseRuns(6, false) },
  ],
  buildTasks: denseTasks,
  steps: [denseFailed, ...denseRunning],
});

// The fixture slices: the data the in-memory fixture transport answers with, one field per answer. shippedSlices is the
// set the transport serves when it is given none.

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import type { Code } from "@connectrpc/connect";

import {
  type ContractsGetResponse,
  type ContractsListResponse,
  ContractsListResponseSchema,
} from "../gen/orrery/v1/contracts_pb";
import {
  type EpicsGetResponse,
  type EpicsHistoryResponse,
  type EpicsListResponse,
  EpicsListResponseSchema,
} from "../gen/orrery/v1/epics_pb";
import {
  type HostStatusResponse,
  type ListHeadlessResponse,
  ListHeadlessResponseSchema,
  type ListResponse,
  type PanesListResponse,
  PanesListResponseSchema,
  type PickerRowSchema,
  type SessionsWatchConversationResponse,
  type WatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
import { contracts } from "./contracts";
import { tideAccepted, tideProposed, tideReady, tideSetAside } from "./contractrecords";
import {
  buoyPlanning,
  buoyPlanningHistory,
  denseEpic,
  denseGet,
  denseHistory,
  denseSteering,
  type EpicRow,
  epicsListWith,
  evidenceArtifacts,
  type EvidenceArtifactBytes,
  evidenceLogs,
  epics,
  gets,
  histories,
  noticesParked,
  offlineStopped,
  offlineStoppedHistory,
  orbitReleasing,
  rotaBuilding,
  rotaEvidence,
  rotaLimited,
  rotaPlanner,
  rotaProblem,
  rotaReviewing,
  tidecacheGaps,
  tidecacheGapsRow,
  tidecacheGapsHistory,
} from "./epics";
import { host } from "./host";
import { panes, paneStreams } from "./panes";
import { headless, sessions, sessionStreams } from "./sessions";
import { taskRecordStories } from "./taskrecord";

// PickerRowInit is one row of a Picker.Ask answer, as a plain object.
export type PickerRowInit = MessageInitShape<typeof PickerRowSchema>;

// AskAnswer is what Picker.Ask answers with for the words it is asked: the rows it finds, a call held open until it
// aborts, or a failure with the Connect code the call fails with.
export type AskAnswer =
  | { readonly rows: readonly PickerRowInit[] }
  | { readonly held: true }
  | { readonly failure: Code };

// Slices is the data a fixture transport answers with. evidenceLogs holds the log each run's Evidence.Artifact answers
// with, by run id. asks holds the answer Picker.Ask gives for each words, and words with no entry are refused.
// contracts is the Contracts.List answer, and contractGets holds the Contracts.Get answer for each contract id. fail holds
// the procedures that answer Unavailable, and hold the procedures that stay unanswered until their call aborts, each named
// as "orrery.v1.Panes/List".
export interface Slices {
  readonly epics: EpicsListResponse;
  readonly contracts: ContractsListResponse;
  readonly contractGets: Readonly<Record<string, ContractsGetResponse>>;
  readonly gets: ReadonlyMap<string, EpicsGetResponse>;
  readonly histories: ReadonlyMap<string, EpicsHistoryResponse>;
  readonly panes: PanesListResponse;
  readonly sessions: ListResponse;
  readonly headless: ListHeadlessResponse;
  readonly host: HostStatusResponse;
  readonly paneStreams: ReadonlyMap<string, WatchConversationResponse>;
  readonly sessionStreams: ReadonlyMap<string, SessionsWatchConversationResponse>;
  readonly evidenceLogs: ReadonlyMap<string, string>;
  // evidenceArtifacts holds the bytes of each kept artifact Evidence.Artifact answers with, by evidenceArtifactKey.
  readonly evidenceArtifacts: ReadonlyMap<string, EvidenceArtifactBytes>;
  // say is how the fixture answers Control.Say. refused fails it as a session that takes no messages does, and unanswered
  // fails it as a call with no answer does. Unset, the write is absorbed.
  readonly say?: "refused" | "unanswered";
  // interrupt is how the fixture answers Control.Interrupt: with texts, the messages it cancels from the queue. then is the
  // next frame each open conversation stream of the session sends once the interrupt is called, after which the stream
  // stays open until its call aborts. Unset, the interrupt is absorbed and no texts come back.
  readonly interrupt?: { texts: string[]; then?: SessionsWatchConversationResponse };
  // steps is how the fixture answers the step calls: hold refuses Steps.Hold as a step that has ended does, and nudge fails
  // Steps.Nudge as a call with no answer does. Unset, each call is absorbed.
  readonly steps?: { readonly hold?: "refused"; readonly nudge?: "unanswered" };
  // keysThen is the next frame each open conversation stream of a pane sends once Panes.SendKeys is called for that pane,
  // by pane id, after which the stream stays open until its call aborts. Unset, the keys are absorbed and no frame comes.
  readonly keysThen?: ReadonlyMap<string, WatchConversationResponse>;
  readonly asks: ReadonlyMap<string, AskAnswer>;
  // heldGets names the epics whose Epics.Get stays open until its call aborts.
  readonly heldGets: ReadonlySet<string>;
  // failingEpicCommands names the epic commands that fail, each with the reason its error carries.
  readonly failingEpicCommands: ReadonlyMap<"pause" | "resume" | "stop" | "retry", string>;
  readonly fail?: ReadonlySet<string>;
  readonly hold?: ReadonlySet<string>;
}

// shippedSlices is the data the fixture transport serves when it is given no slices.
export const shippedSlices: Slices = {
  epics,
  contracts,
  contractGets: { "tidepool/tide-alerts": tideProposed },
  gets,
  histories,
  panes,
  sessions,
  headless,
  host,
  paneStreams,
  sessionStreams,
  evidenceLogs,
  evidenceArtifacts,
  asks: new Map<string, AskAnswer>(),
  heldGets: new Set<string>(),
  failingEpicCommands: new Map<"pause" | "resume" | "stop" | "retry", string>(),
};

// EpicScenario names an epic page scenario of round 6 that the page stories draw over the slices it is given.
export type EpicScenario =
  | "building"
  | "limited"
  | "plannerParked"
  | "commandError"
  | "problem"
  | "unavailable"
  | "planning"
  | "dense"
  | "loading"
  | "parked"
  | "evidence"
  | "reviewing"
  | "releasing"
  | "releaseFailed"
  | "endedGaps";

const rotaId = "lighthouse/rota-export";
const buoyId = "tidepool/buoy-feeds";
const tideCacheId = "tidepool/tide-cache";
const denseId = "commonframe/deck-pipeline";
const noticesId = "orrery/notices-usable";
const orbitId = "orrery/orbit-labels";
const offlineId = "tidepool/offline-tables";

// EpicState is an epic in one state the page scenarios draw: its Epics.Get answer, its list row when the state changes it,
// and its history when the state has one.
interface EpicState {
  readonly get: EpicsGetResponse;
  readonly row?: EpicRow;
  readonly history?: EpicsHistoryResponse;
}

// withEpic is the shipped slices with one epic in a state: its list row replaced when the state has one, its Epics.Get
// answer replaced, and its history replaced when the state has one.
function withEpic(id: string, state: EpicState): Slices {
  return {
    ...shippedSlices,
    epics: epicsListWith((row) => (row.id === id && state.row !== undefined ? state.row : row)),
    gets: new Map<string, EpicsGetResponse>([...gets, [id, state.get]]),
    histories:
      state.history === undefined
        ? histories
        : new Map<string, EpicsHistoryResponse>([...histories, [id, state.history]]),
  };
}

// denseSteererPanes are the shipped panes with commonframe's pane %21 listed as the session that steers deck-pipeline. The
// shipped %21 carries no orrery id, so the list draws it as no steerer.
const denseSteererPanes = {
  ...panes,
  panes: panes.panes.map((pane) => (pane.id === "%21" ? { ...pane, orreryId: denseSteering } : pane)),
};

// epicScenarios holds the slices each epic page scenario draws over. A scenario's slices change only what it names, and
// the shipped slices serve every other story and test.
export const epicScenarios: Record<EpicScenario, Slices> = {
  building: withEpic(rotaId, rotaBuilding),
  reviewing: withEpic(rotaId, rotaReviewing),
  releasing: withEpic(orbitId, { get: orbitReleasing }),
  releaseFailed: withEpic(offlineId, { get: offlineStopped, history: offlineStoppedHistory }),
  endedGaps: withEpic(tideCacheId, { get: tidecacheGaps, row: tidecacheGapsRow, history: tidecacheGapsHistory }),
  evidence: withEpic(rotaId, rotaEvidence),
  limited: withEpic(rotaId, rotaLimited),
  plannerParked: withEpic(rotaId, rotaPlanner),
  commandError: {
    ...withEpic(rotaId, rotaBuilding),
    failingEpicCommands: new Map<"pause" | "resume" | "stop" | "retry", string>([["pause", "the server did not answer"]]),
  },
  problem: withEpic(rotaId, rotaProblem),
  unavailable: {
    ...withEpic(rotaId, rotaBuilding),
    gets: new Map<string, EpicsGetResponse>([...gets].filter(([id]) => id !== rotaId)),
  },
  planning: {
    ...shippedSlices,
    gets: new Map<string, EpicsGetResponse>([...gets, [buoyId, buoyPlanning]]),
    histories: new Map<string, EpicsHistoryResponse>([...histories, [buoyId, buoyPlanningHistory]]),
  },
  dense: {
    ...shippedSlices,
    panes: denseSteererPanes,
    epics: epicsListWith((row) => row, [denseEpic]),
    gets: new Map<string, EpicsGetResponse>([...gets, [denseId, denseGet]]),
    histories: new Map<string, EpicsHistoryResponse>([...histories, [denseId, denseHistory]]),
  },
  parked: { ...shippedSlices, gets: new Map<string, EpicsGetResponse>([...gets, [noticesId, noticesParked]]) },
  loading: {
    ...shippedSlices,
    epics: epicsListWith((row) => (row.id === tideCacheId ? { ...row, seen: 0n } : row)),
    heldGets: new Set([tideCacheId]),
  },
};

// contractSlices are the shipped slices with the record of one contract served, and the List holding that contract's own
// entry, so the List and the record agree on its state and version.
function contractSlices(id: string, record: ContractsGetResponse): Slices {
  return {
    ...shippedSlices,
    contracts: create(ContractsListResponseSchema, { contracts: record.contract === undefined ? [] : [record.contract] }),
    contractGets: { [id]: record },
  };
}

// contractStories are the slices the contract page stories are served from, one for each state the scenarios show: the
// record of tide-alerts in that state, and its List entry at the same state.
export const contractStories = {
  toAccept: contractSlices("tidepool/tide-alerts", tideProposed),
  accepted: contractSlices("tidepool/tide-alerts", tideAccepted),
  setAside: contractSlices("tidepool/tide-alerts", tideSetAside),
  ready: contractSlices("tidepool/tide-alerts", tideReady),
};

// taskRecordSlices are the slices the task record and worker session page stories are served from, by scenario.
export const taskRecordSlices = taskRecordStories({ building: epicScenarios.building, shipped: shippedSlices });

// emptySlices is the world with nothing running: no panes, no headless sessions, no epics and no contracts. Every other
// answer is the shipped one.
export const emptySlices: Slices = {
  ...shippedSlices,
  epics: create(EpicsListResponseSchema, { epics: [] }),
  contracts: create(ContractsListResponseSchema, { contracts: [] }),
  contractGets: {},
  panes: create(PanesListResponseSchema, { panes: [] }),
  headless: create(ListHeadlessResponseSchema, { sessions: [] }),
};

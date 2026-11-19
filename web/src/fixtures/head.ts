// The fixture slice variants the head's page stories draw: a host under load, a warm-up session with its background tasks
// running, an epic steered by a pane, and an epic list steered by a session. Each is a copy of shippedSlices with the
// answers it changes replaced, so the shipped slices are never changed.

import { clone, create } from "@bufbuild/protobuf";

import {
  BackgroundTaskSchema,
  ConversationContextSchema,
  ConversationItemSchema,
  HostStatusResponseSchema,
  PanesListResponseSchema,
  SessionsWatchConversationResponseSchema,
  WatchConversationResponseSchema,
  type ConversationState,
  type SessionsWatchConversationResponse,
} from "../gen/orrery/v1/orrery_pb";
import { EpicSchema, EpicsGetResponseSchema, EpicsListResponseSchema, type EpicsListResponse } from "../gen/orrery/v1/epics_pb";
import { at } from "./clock";
import { call, type ItemInit } from "./panes";
import { shippedSlices, type Slices } from "./slices";

// must returns value, and throws when it is missing: the shipped fixtures hold every answer a variant changes.
function must<T>(value: T | undefined, what: string): T {
  if (value === undefined) {
    throw new Error(`fixtures: the shipped fixtures hold no ${what}`);
  }
  return value;
}

// replaced returns a copy of the answers with the answer under key replaced.
function replaced<Answer>(answers: ReadonlyMap<string, Answer>, key: string, answer: Answer): Map<string, Answer> {
  return new Map(answers).set(key, answer);
}

// warmupSession is the id of the warm-up headless session: the one the headless list holds under the warm-up's label.
export const warmupSession = must(
  shippedSlices.headless.sessions.find((session) => session.label === "Tide table cache warm-up"),
  "headless session Tide table cache warm-up",
).id;

const rotaId = "lighthouse/rota-export";

// steered returns the shipped epic list with each epic that steering names steered by the session it names.
function steered(steering: ReadonlyMap<string, string>): EpicsListResponse {
  return create(EpicsListResponseSchema, {
    epics: shippedSlices.epics.epics.map((epic) => {
      const by = steering.get(epic.id);
      if (by === undefined) {
        return epic;
      }
      const copy = clone(EpicSchema, epic);
      copy.steering = by;
      return copy;
    }),
  });
}

// withOrreryId returns the slices with the pane of paneId given orreryId, in the pane list and in the pane's stream.
function withOrreryId(slices: Slices, paneId: string, orreryId: string): Slices {
  const panes = clone(PanesListResponseSchema, slices.panes);
  must(panes.panes.find((pane) => pane.id === paneId), `pane ${paneId}`).orreryId = orreryId;
  const stream = clone(
    WatchConversationResponseSchema,
    must(slices.paneStreams.get(paneId), `stream of pane ${paneId}`),
  );
  must(must(stream.state, `state of pane ${paneId}'s stream`).pane, `pane of pane ${paneId}'s stream`).orreryId = orreryId;
  return { ...slices, panes, paneStreams: replaced(slices.paneStreams, paneId, stream) };
}

// warmupCopy is a copy of the shipped warm-up stream, to change.
function warmupCopy(): SessionsWatchConversationResponse {
  return clone(
    SessionsWatchConversationResponseSchema,
    must(shippedSlices.sessionStreams.get(warmupSession), "stream of the warm-up session"),
  );
}

// warmupStateOf is the state of a warm-up stream. It throws when the stream holds none.
function warmupStateOf(stream: SessionsWatchConversationResponse): ConversationState {
  return must(stream.state, "state of the warm-up session");
}

// hotWarmup is the warm-up session with its context near the window: 174000 of 200000 tokens.
function hotWarmup(): SessionsWatchConversationResponse {
  const stream = warmupCopy();
  warmupStateOf(stream).context = create(ConversationContextSchema, { tokens: 174000n, window: 200000n });
  return stream;
}

const warmupGoal = "Every port’s tide table is cached through December 17 and the warm-up is one command.";

// The two agents and the shell the warm-up session has running in the background.
const runningTasks = [
  create(BackgroundTaskSchema, {
    id: "agent-explore", toolUseId: "toolu_agent_explore", description: "Find every caller of keeperFor",
    type: "agent", subagentType: "Explore", status: "running", activity: "Grep keeperFor\\(",
  }),
  create(BackgroundTaskSchema, {
    id: "agent-reviewer", toolUseId: "toolu_agent_reviewer", description: "Review the query change",
    type: "agent", subagentType: "reviewer", status: "running", activity: "Read query.go",
  }),
  create(BackgroundTaskSchema, {
    id: "shell-dev", toolUseId: "toolu_shell_dev", description: "pnpm dev --port 5173",
    type: "shell", subagentType: "", status: "running", activity: "ready on :5173",
  }),
];

// The warm-up's later calls, one per port, after the reply the shipped transcript ends on. Their ids continue the
// shipped transcript's, which uses toolu_1 to toolu_4.
const ports = ["faro", "porto", "cadiz", "vigo", "brest", "cork", "bergen", "aberdeen", "hull", "dover", "bilbao", "lerwick"];
const scrollingItems: ItemInit[] = ports.flatMap((port, index) =>
  call(5 + index, at("@-2d 14:10") + BigInt(index) * 20n, "Bash", `just warm --port ${port}`, `${port} is warm through December 17`),
);

// runningWarmup is the warm-up session as a run in progress has it: its goal, the tasks it has running, and a transcript
// long enough to scroll at phone size.
function runningWarmup(): SessionsWatchConversationResponse {
  const stream = warmupCopy();
  const state = warmupStateOf(stream);
  state.goal = warmupGoal;
  state.background = runningTasks;
  stream.items.push(...scrollingItems.map((item) => create(ConversationItemSchema, item)));
  return stream;
}

// hotSlices: the host under load, cpu at warn and ram at crit, and the warm-up session near its context window.
export const hotSlices: Slices = {
  ...shippedSlices,
  host: create(HostStatusResponseSchema, {
    host: shippedSlices.host.host,
    stats: [
      { label: "cpu", value: "71%", percent: 71, level: "warn" },
      { label: "ram", value: "30G", percent: 94, level: "crit" },
      { label: "disk", value: "49%", percent: 49, level: "ok" },
      { label: "net", value: "↓1M ↑298K", level: "ok" },
    ],
  }),
  sessionStreams: replaced(shippedSlices.sessionStreams, warmupSession, hotWarmup()),
};

// fullSlices: the warm-up session running, and the rota-export epic it steers, with its question open.
export const fullSlices: Slices = {
  ...shippedSlices,
  epics: steered(new Map([[rotaId, warmupSession]])),
  sessionStreams: replaced(shippedSlices.sessionStreams, warmupSession, runningWarmup()),
};

// rotaNoQuestion is the rota-export detail with its open question answered and gone: nothing asks.
const rotaNoQuestion = clone(
  EpicsGetResponseSchema,
  must(shippedSlices.gets.get(rotaId), "Epics.Get answer of rota-export"),
);
rotaNoQuestion.questions = [];

// scrolledSlices: fullSlices with nothing asking.
export const scrolledSlices: Slices = {
  ...fullSlices,
  gets: replaced(fullSlices.gets, rotaId, rotaNoQuestion),
};

// epicSlices: pane %14 is the pane that steers rota-export, and rota-export is steered by that pane's orrery id.
export const epicSlices: Slices = withOrreryId(
  { ...shippedSlices, epics: steered(new Map([[rotaId, "rotaplan"]])) },
  "%14",
  "rotaplan",
);

// manySlices: pane %22 steers four epics, of which only notices-usable has a need, a park.
const followplan = "followplan";
export const manySlices: Slices = withOrreryId(
  {
    ...shippedSlices,
    epics: steered(new Map([
      ["orrery/orrery-followups", followplan],
      ["orrery/orbit-labels", followplan],
      ["orrery/notices-usable", followplan],
      ["orrery/harbour-map", followplan],
    ])),
  },
  "%22",
  followplan,
);

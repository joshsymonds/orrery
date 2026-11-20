// The stories of ListPanel: a conversation's running sub-agents, and its shells left running, each inside the side panel
// frame it opens in. The tasks are set at the fixtures' instant, so each clock reads the same on every run.

import { create } from "@bufbuild/protobuf";

import { at, liveEpoch } from "./fixtures/clock";
import { type BackgroundTask, BackgroundTaskSchema } from "./gen/orrery/v1/orrery_pb";
import type { Stories } from "./catalog/story";
import { formatAddress } from "./router";
import { ListPanel as Lists, SidePanel } from "./sidepanel";

// now is the instant the fixtures are set at, in unix seconds, as the lists' clocks read it.
const now = Date.parse(liveEpoch) / 1000;

// inbox is the address the panels' Open in a new window link names.
const inbox = formatAddress({ order: "attention", entry: null });

const agents: BackgroundTask[] = [
  create(BackgroundTaskSchema, {
    id: "agent-1",
    description: "Map the rota's holds",
    type: "agent",
    subagentType: "Explore",
    status: "running",
    activity: "Reading the holder scope",
    started: at("@14:37:58"),
  }),
  create(BackgroundTaskSchema, {
    id: "agent-2",
    description: "Check the contract tide",
    type: "agent",
    status: "running",
    activity: "Running the tide check",
    started: at("@14:40:30"),
  }),
];

const running: BackgroundTask[] = [
  create(BackgroundTaskSchema, {
    id: "shell-1",
    description: "Watch the build",
    type: "shell",
    status: "running",
    activity: "tail -f build.log",
    started: at("@14:30:00"),
  }),
];

export const ListPanel: Stories = {
  agents: () => (
    <SidePanel label="Sub-agents" tone="live" href={inbox} onClose={() => undefined}>
      <Lists kind="agents" tasks={agents} now={now} />
    </SidePanel>
  ),
  running: () => (
    <SidePanel label="Left running" tone="quiet" href={inbox} onClose={() => undefined}>
      <Lists kind="running" tasks={running} now={now} />
    </SidePanel>
  ),
};

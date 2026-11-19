// The stories of BackgroundRow: an agent task that runs, which is a link to its Agent call; an agent task that has ended;
// an agent task that names no sub-agent type; and a shell task, which is not a link.

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import * as backgroundrow from "./backgroundrow";
import type { Stories } from "./catalog/story";
import { at } from "./fixtures/clock";
import { type BackgroundTask, BackgroundTaskSchema } from "./gen/orrery/v1/orrery_pb";

const ignore = () => {};

// taskOf is a background task of the given fields, as the headless session's conversation state reports one.
function taskOf(init: MessageInitShape<typeof BackgroundTaskSchema>): BackgroundTask {
  return create(BackgroundTaskSchema, init);
}

export const BackgroundRow: Stories = {
  "agent-running": () => (
    <backgroundrow.BackgroundRow
      task={taskOf({
        id: "bg-1",
        toolUseId: "toolu_7",
        description: "map the rota package",
        type: "agent",
        subagentType: "Explore",
        status: "running",
        activity: "Grep rota.*Export",
        started: at("@14:40"),
      })}
      onOpen={ignore}
    />
  ),
  "agent-done": () => (
    <backgroundrow.BackgroundRow
      task={taskOf({
        id: "bg-2",
        toolUseId: "toolu_9",
        description: "review export.go for overwrites",
        type: "agent",
        subagentType: "general-purpose",
        status: "done",
        started: at("@14:12"),
        ended: at("@14:18"),
      })}
      onOpen={ignore}
    />
  ),
  "agent-failed": () => (
    <backgroundrow.BackgroundRow
      task={taskOf({
        id: "bg-3",
        toolUseId: "toolu_11",
        description: "check the csv writer",
        type: "agent",
        status: "failed",
        activity: "timed out waiting for a reply",
        started: at("@14:20"),
        ended: at("@14:24"),
      })}
      onOpen={ignore}
    />
  ),
  "shell-running": () => (
    <backgroundrow.BackgroundRow
      task={taskOf({
        id: "bg-4",
        toolUseId: "",
        description: "go test ./cli/...",
        type: "shell",
        status: "running",
        started: at("@14:30"),
      })}
      onOpen={ignore}
    />
  ),
};

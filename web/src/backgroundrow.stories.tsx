// The stories of TaskLine: the lines BackgroundRow draws for a background task, which round 6 names TaskLine (catalog/
// coverage.ts storyRenames): an agent task that has ended; an agent task that failed; and a shell task that was stopped.

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import * as backgroundrow from "./backgroundrow";
import { type Stories, inReader } from "./catalog/story";
import { at } from "./fixtures/clock";
import { type BackgroundTask, BackgroundTaskSchema } from "./gen/orrery/v1/orrery_pb";

const ignore = () => {};

// taskOf is a background task of the given fields, as the headless session's conversation state reports one.
function taskOf(init: MessageInitShape<typeof BackgroundTaskSchema>): BackgroundTask {
  return create(BackgroundTaskSchema, init);
}

export const TaskLine: Stories = inReader({
  finished: () => (
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
  failed: () => (
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
  stopped: () => (
    <backgroundrow.BackgroundRow
      task={taskOf({
        id: "bg-5",
        toolUseId: "",
        description: "pnpm dev --port 5173",
        type: "shell",
        status: "stopped",
        started: at("@14:12"),
        ended: at("@14:30"),
      })}
      onOpen={ignore}
    />
  ),
});

// The stories of TaskTable: the tasks of the rota fixture, drawn at the fixtures' instant.

import type { Stories } from "./catalog/story";
import { liveEpoch } from "./fixtures/clock";
import { gets } from "./fixtures/epics";
import { recordTasksOf } from "./record";
import * as table from "./tasktable";

const now = Date.parse(liveEpoch) / 1000;

const noop = () => {};

// rotaTasks are the record tasks of the rota fixture epic.
function rotaTasks() {
  const detail = gets.get("lighthouse/rota-export");
  if (detail === undefined) {
    throw new Error("the fixtures hold no detail for lighthouse/rota-export");
  }
  return recordTasksOf(detail);
}

export const TaskTable: Stories = {
  default: () => <table.TaskTable tasks={rotaTasks()} now={now} onOpenPhase={noop} />,
};

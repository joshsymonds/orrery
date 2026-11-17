import type { Epic, EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { parseQuestion } from "./question";
import type { Row } from "./rows";
import { blockerOf } from "./status";

// A need's kind: a waiting row, an open question, a failed release step, a park, or any other blocker.
export type NeedKind = "row" | "question" | "release" | "park" | "other";

// One thing waiting on the user, as a sentence. A row's summary is its why, which the row's title draws, so the row's
// sentence does not repeat it. An epic's sentence already says why, so its summary is empty. An epic need names its epic, and a question need its question's id; a
// row need names neither.
export interface Need {
  kind: NeedKind;
  text: string;
  summary: string;
  epic: string;
  question: string;
}

// The state of a release step that did not pass.
const releaseFailed = "failed";

// rowNeed is the need of a waiting row: the question the row holds, which it asks, else that it waits on you. The row's
// title already draws its directory and its summary, so the sentence names neither. Its summary is the why.
export function rowNeed(row: Row): Need {
  const text = row.question === "" ? "waits on you" : `asks: ${parseQuestion(row.question).title}`;
  return { kind: "row", text, summary: row.summary, epic: "", question: "" };
}

// epicNeeds raises the needs of each live epic with a detail, in list order. Each open question is a question need,
// even when the planner holds the epic's park. A planner-parked epic (plannerParked) raises no release, park or other
// need from that park: the planner answers it. For any other epic, the epic's first failed release step is a release
// need, and it replaces the park. Otherwise a park is a park need, and any other blocker is an other need with its own
// text. The blocker is read without the questions, which are raised first, and without the park, which the branches
// above already answer.
export function epicNeeds(epics: Epic[], details: Map<string, EpicsGetResponse>): Need[] {
  const needs: Need[] = [];
  for (const epic of epics) {
    const detail = details.get(epic.id);
    if (!epic.live || detail === undefined) {
      continue;
    }
    for (const question of detail.questions) {
      needs.push(epicNeed("question", epic.id, `${epic.slug} asks: ${question.text}`, question.id));
    }
    const failed = detail.plannerParked
      ? undefined
      : detail.releaseSteps.find((proof) => proof.state === releaseFailed);
    if (failed !== undefined) {
      needs.push(
        epicNeed("release", epic.id, `${epic.slug}: release step ${failed.position} (${failed.step}) failed: ${failed.reason}`),
      );
    } else if (detail.parked !== "" && !detail.plannerParked) {
      needs.push(epicNeed("park", epic.id, `${epic.slug} is parked: ${detail.parked}`));
    } else {
      const blocker = blockerOf({ ...detail, parked: "", questions: [] });
      if (blocker !== null) {
        needs.push(epicNeed("other", epic.id, `${epic.slug}: ${blocker.text}`));
      }
    }
  }
  return needs;
}

function epicNeed(kind: NeedKind, epic: string, text: string, question = ""): Need {
  return { kind, text, summary: "", epic, question };
}

import type { EpicHistoryEntry, EpicQuestion, EpicsGetResponse, EpicStep } from "./gen/orrery/v1/epics_pb";
import { releaseOf } from "./ending";
import { durationText, tasksOf } from "./phases";
import { recordTasksOf } from "./record";
import { clock } from "./reader";
import { stateOf } from "./status";

// The words and tones of an epic's record: its track, its state and blocker pills, its contract door, the sections it
// has reached, and Now's sentence, what asks, and its running rows. A component draws these and decides nothing.
export type WordTone = "live" | "attention" | "quiet" | "engine" | "ok" | "bad";
export type StageName = "Contract" | "Build" | "Review" | "Release" | "Report";
export interface Stage {
  name: StageName;
  state: "done" | "now" | "ahead";
  tone: WordTone;
  words: string;
}
export interface Pill {
  word: string;
  tone: WordTone;
}
export interface Reached {
  summary: boolean;
  track: boolean;
  now: boolean;
  history: boolean;
  tasks: boolean;
  requirements: boolean;
}
export type Asking =
  | { kind: "question"; question: EpicQuestion }
  | { kind: "park"; reason: string }
  | { kind: "planner"; line: string }
  | { kind: "steerer"; line: string }
  | { kind: "limited" }
  | null;
export interface StepLine {
  id: string;
  glyph: "working" | "held" | "limited";
  label: string;
  task: string;
  doing: string;
  since: number;
}

const stageNames: readonly StageName[] = ["Contract", "Build", "Review", "Release", "Report"];

// The index of the stage an orreryd phase is in: Contract for a phase no stage names.
function stageIndexOf(phase: string): number {
  switch (phase) {
    case "accepted":
    case "built":
      return 1;
    case "reviewing":
    case "correcting":
      return 2;
    case "clean":
    case "releasing":
    case "stopped":
      return 3;
    case "released":
    case "done":
    case "met_at_base":
    case "ended_with_gaps":
    case "closed_superseded":
    case "closed_shipped":
    case "closed_abandoned":
      return 4;
    default:
      return 0;
  }
}

const reportIndex = stageIndexOf("released");

function isOrreryd(detail: EpicsGetResponse): boolean {
  return detail.epic?.source === "orreryd";
}

function phaseOf(detail: EpicsGetResponse): string {
  return detail.epic?.outcome ?? "";
}

function runningSteps(detail: EpicsGetResponse): EpicStep[] {
  return detail.steps.filter((step) => step.end === "");
}

// A step's name: its task, else its agent.
function stepName(step: EpicStep): string {
  return step.task !== "" ? step.task : step.agent;
}

// The distinct task labels among the running steps.
function runningTasks(detail: EpicsGetResponse): number {
  return new Set(runningSteps(detail).map((step) => step.task).filter((task) => task !== "")).size;
}

// Whether the epic is parked and the park waits on the owner: neither its planner nor its steerer holds it.
export function ownerParked(detail: EpicsGetResponse): boolean {
  return detail.parked !== "" && !detail.plannerParked && !detail.steererParked;
}

export function plannerParked(detail: EpicsGetResponse): boolean {
  return detail.plannerParked;
}

// Whether the epic is parked and its steerer holds the park, until it escalates the park's question to the owner.
export function steererParked(detail: EpicsGetResponse): boolean {
  return detail.steererParked;
}

// The open questions the owner is asked: every one its steerer does not hold.
function ownerQuestions(detail: EpicsGetResponse): EpicQuestion[] {
  return detail.questions.filter((question) => !question.steererHeld);
}

// The open questions the epic's steerer holds, which ask the owner nothing until the steerer escalates them.
function steererQuestions(detail: EpicsGetResponse): EpicQuestion[] {
  return detail.questions.filter((question) => question.steererHeld);
}

// The quiet line of a question the epic's steerer holds, as the record and a conversation's event ring draw it.
export function steererQuestionLine(question: EpicQuestion): string {
  return `Its steerer holds this question, and answers it or escalates it to you: ${question.text}`;
}

// Whether at least one step runs and every running step is held by a usage limit.
export function allLimited(detail: EpicsGetResponse): boolean {
  const running = runningSteps(detail);
  return running.length > 0 && running.every((step) => step.limited);
}

// Whether something asks the owner: an open question its steerer does not hold, or a park the owner holds.
function asksYou(detail: EpicsGetResponse): boolean {
  return ownerQuestions(detail).length > 0 || ownerParked(detail);
}

// The tone of a live phase: grey when every running step is limited, else live.
function liveTone(detail: EpicsGetResponse): WordTone {
  return allLimited(detail) ? "quiet" : "live";
}

// The state word and tone of an orreryd epic, without its time.
function orrerydPill(detail: EpicsGetResponse): Pill {
  if (detail.paused) {
    return { word: "paused", tone: "quiet" };
  }
  if (ownerParked(detail)) {
    return { word: "parked", tone: "attention" };
  }
  const phase = phaseOf(detail);
  switch (phase) {
    case "accepted":
      return { word: tasksOf(detail).length === 0 ? "planning" : "building", tone: liveTone(detail) };
    case "built":
      return { word: "building", tone: liveTone(detail) };
    case "reviewing":
    case "correcting":
      return { word: "reviewing", tone: liveTone(detail) };
    case "clean":
      return { word: "ready to release", tone: "quiet" };
    case "releasing":
      return { word: "releasing", tone: "engine" };
    case "stopped":
      return { word: "stopped", tone: "bad" };
    case "released":
      return { word: "released", tone: "ok" };
    case "done":
      return { word: "done", tone: "ok" };
    case "met_at_base":
      return { word: "already met", tone: "quiet" };
    case "ended_with_gaps":
      return { word: "ended with gaps", tone: "attention" };
    case "closed_superseded":
      return { word: "superseded", tone: "quiet" };
    case "closed_shipped":
      return { word: "shipped outside orrery", tone: "ok" };
    case "closed_abandoned":
      return { word: "abandoned", tone: "quiet" };
    default:
      return { word: phase, tone: "quiet" };
  }
}

// The track of an orreryd epic: five stages, the one its phase is in, and the words of each. A record epic has none.
export function trackOf(detail: EpicsGetResponse): Stage[] {
  if (!isOrreryd(detail)) {
    return [];
  }
  const phase = phaseOf(detail);
  const now = stageIndexOf(phase);
  return stageNames.map((name, index) => {
    if (index > now) {
      return { name, state: "ahead", tone: "quiet", words: "not reached" };
    }
    const state = index === now ? "now" : "done";
    switch (name) {
      case "Contract":
        return state === "now"
          ? { name, state, tone: "live", words: phase }
          : { name, state, tone: "ok", words: acceptedWords(detail) };
      case "Build":
        return buildStage(detail, state);
      case "Review":
        if (state === "done") {
          return { name, state, tone: "ok", words: "clean" };
        }
        return { name, state, tone: "live", words: phase === "correcting" ? "correcting" : "reviewing" };
      case "Release":
        if (state === "done") {
          return { name, state, tone: "ok", words: "released" };
        }
        return releaseNow(detail);
      case "Report":
        return reportStage(detail);
    }
  });
}

// The contract's accepted time as the Contract stage reads it, just "accepted" when the time is not known.
function acceptedWords(detail: EpicsGetResponse): string {
  const accepted = detail.contract?.accepted ?? 0n;
  return accepted === 0n ? "accepted" : `accepted ${clock(accepted)}`;
}

// The Build stage: its task count, and while it is now, what asks, whether it is paused, and whether a step waits on a
// limit.
function buildStage(detail: EpicsGetResponse, state: "done" | "now"): Stage {
  const tasks = recordTasksOf(detail);
  if (state === "now" && tasks.length === 0) {
    return { name: "Build", state, tone: buildTone(detail), words: "the planner is outlining tasks" };
  }
  const done = tasks.filter((task) => task.place === "integrated").length;
  let words = `${done} of ${tasks.length} tasks done`;
  if (state === "now") {
    if (asksYou(detail)) {
      words += " · 1 waits on you";
    }
    if (detail.paused) {
      words += " · paused";
    }
    if (runningSteps(detail).some((step) => step.limited)) {
      words += " · waiting on a limit";
    }
    return { name: "Build", state, tone: buildTone(detail), words };
  }
  return { name: "Build", state, tone: "ok", words };
}

// The tone of Build while it is now: attention when something asks, else quiet when paused or every step is limited.
function buildTone(detail: EpicsGetResponse): WordTone {
  if (asksYou(detail)) {
    return "attention";
  }
  if (detail.paused || allLimited(detail)) {
    return "quiet";
  }
  return "live";
}

// The Release stage while it is now: clean, releasing or stopped.
function releaseNow(detail: EpicsGetResponse): Stage {
  switch (phaseOf(detail)) {
    case "releasing":
      return { name: "Release", state: "now", tone: "engine", words: "releasing" };
    case "stopped":
      return { name: "Release", state: "now", tone: "bad", words: "stopped" };
    default:
      return { name: "Release", state: "now", tone: "quiet", words: "ready to release" };
  }
}

// The Report stage: not reached until the epic ends, then its plain state word.
function reportStage(detail: EpicsGetResponse): Stage {
  if (stageIndexOf(phaseOf(detail)) < reportIndex) {
    return { name: "Report", state: "ahead", tone: "quiet", words: "not reached" };
  }
  const pill = orrerydPill(detail);
  return { name: "Report", state: "now", tone: pill.tone, words: pill.word };
}

// The state pill: the epic's state word and tone, with its time since the phase began once it has begun.
export function statePillOf(detail: EpicsGetResponse, now: number): Pill {
  const pill = isOrreryd(detail) ? orrerydPill(detail) : { word: stateOf(detail), tone: "quiet" as const };
  const phaseAt = Number(detail.epic?.phaseAt ?? 0);
  if (pill.word === "" || phaseAt <= 0) {
    return pill;
  }
  return { word: `${pill.word} · ${durationText(Math.max(0, now - phaseAt))}`, tone: pill.tone };
}

// The blocker pill, first match: what asks the owner, in attention (an open question with its time, an owner's park, a
// step the owner held), then a park its steerer holds, quiet, then the release or close the owner waits on, then a
// question its steerer holds with its time, quiet. The steerer's park comes ahead of the ending because the park holds
// the stopped release, which the owner is not asked about until the steerer escalates it.
export function blockerPillOf(detail: EpicsGetResponse, now: number): Pill | null {
  const question = ownerQuestions(detail)[0];
  if (question !== undefined) {
    return { word: askedWord("asks you a question", question, now), tone: "attention" };
  }
  if (ownerParked(detail)) {
    return { word: "asks you what to do", tone: "attention" };
  }
  const held = runningSteps(detail).find((step) => step.held);
  if (held !== undefined) {
    return { word: `${stepName(held)} held`, tone: "attention" };
  }
  if (steererParked(detail)) {
    return { word: "its steerer holds the park", tone: "quiet" };
  }
  const ending = endingPillOf(detail);
  if (ending !== null) {
    return ending;
  }
  const steerer = steererQuestions(detail)[0];
  if (steerer !== undefined) {
    return { word: askedWord("asks its steerer a question", steerer, now), tone: "quiet" };
  }
  return null;
}

// A question pill's word, with the time since the question was asked once it has an asked time.
function askedWord(word: string, question: EpicQuestion, now: number): string {
  const asked = Number(question.asked);
  return asked > 0 ? `${word} · ${durationText(Math.max(0, now - asked))}` : word;
}

// The blocker pill of an orreryd epic that waits on the owner's release or close: a stopped release's step with no undo,
// or an epic that ended with gaps. Null for every other epic.
function endingPillOf(detail: EpicsGetResponse): Pill | null {
  if (!isOrreryd(detail)) {
    return null;
  }
  const release = releaseOf(detail);
  if (release !== null && release.offer !== 0) {
    return { word: `step ${release.offer} failed and cannot be undone`, tone: "attention" };
  }
  if (phaseOf(detail) === "ended_with_gaps") {
    return { word: "release it anyway, or close it", tone: "attention" };
  }
  return null;
}

// The contract door's line, empty with no contract.
export function contractDoorOf(detail: EpicsGetResponse): string {
  const contract = detail.contract;
  if (contract === undefined) {
    return "";
  }
  return `${acceptedWords(detail)} · ${contract.care} care · ${detail.requirements.length} requirements`;
}

// The summary paragraph, else the contract's title.
function summaryText(detail: EpicsGetResponse): string {
  return detail.summary !== "" ? detail.summary : (detail.contract?.title ?? "");
}

// The sections an epic has reached. A record epic reaches its Now and its tasks only.
export function reachedOf(detail: EpicsGetResponse, history: EpicHistoryEntry[]): Reached {
  const tasks = tasksOf(detail).length > 0;
  if (!isOrreryd(detail)) {
    return { summary: false, track: false, now: true, history: false, tasks, requirements: false };
  }
  return {
    summary: summaryText(detail) !== "",
    track: true,
    now: true,
    history: history.length > 0,
    tasks,
    requirements: detail.requirements.length > 0,
  };
}

// The Now sentence of a releasing or stopped orreryd epic, or null for any other epic.
function releaseSentenceOf(detail: EpicsGetResponse): string | null {
  if (!isOrreryd(detail)) {
    return null;
  }
  const steps = detail.releaseSteps;
  switch (phaseOf(detail)) {
    case "releasing": {
      const running = steps.find((step) => step.state === "running");
      return running === undefined
        ? "Releasing."
        : `Releasing: step ${Number(running.position)} of ${steps.length} is running.`;
    }
    case "stopped": {
      const release = releaseOf(detail);
      if (release === null) {
        return "The release stopped.";
      }
      if (release.offer !== 0) {
        return `The release stopped at step ${release.offer}, and that step has no undo. It waits on you.`;
      }
      const at = steps.find((step) => step.state !== "passed");
      return `The release stopped at step ${at === undefined ? steps.length : Number(at.position)}.`;
    }
    default:
      return null;
  }
}

// The one sentence of Now, the first rule that matches.
export function nowSentenceOf(detail: EpicsGetResponse): string {
  if (ownerParked(detail)) {
    return "Parked. Nothing runs until you answer.";
  }
  if (detail.paused) {
    return "Paused. The steps that were running finish; nothing new starts until you resume.";
  }
  if (plannerParked(detail)) {
    return "The planner holds a park and is deciding what to do. Nothing is asked of you.";
  }
  if (steererParked(detail)) {
    return "Parked. Its steerer holds the question; nothing is asked of you unless it escalates it.";
  }
  if (allLimited(detail)) {
    return limitedSentence(runningSteps(detail));
  }
  const tasks = runningTasks(detail);
  if (ownerQuestions(detail).length > 0) {
    if (tasks === 0) {
      return "It waits on your answer.";
    }
    if (tasks === 1) {
      return "One task is being worked on, and one waits on you.";
    }
    return `${tasks} tasks are being worked on, and one waits on you.`;
  }
  if (steererQuestions(detail).length > 0) {
    if (tasks === 0) {
      return "It waits on its steerer's answer.";
    }
    if (tasks === 1) {
      return "One task is being worked on, and one waits on its steerer.";
    }
    return `${tasks} tasks are being worked on, and one waits on its steerer.`;
  }
  const release = releaseSentenceOf(detail);
  if (release !== null) {
    return release;
  }
  if (isOrreryd(detail) && stageIndexOf(phaseOf(detail)) === reportIndex) {
    return `It ended: ${orrerydPill(detail).word}.`;
  }
  if (isOrreryd(detail) && tasksOf(detail).length === 0) {
    return "The planner is reading the contract and outlining tasks. No task has started.";
  }
  if (tasks === 0) {
    return "Nothing is running.";
  }
  if (tasks === 1) {
    return "One task is being worked on.";
  }
  return `${tasks} tasks are being worked on.`;
}

// The sentence of Now when every running step is limited, with the first limited step's kind and reset.
function limitedSentence(running: EpicStep[]): string {
  const first = running[0];
  const subject = running.length === 1 ? "1 step waits" : `${running.length} steps wait`;
  const kind = first.limitKind === "" ? "a usage limit" : `the ${first.limitKind} limit`;
  const reset = first.limitResets > 0n ? `, which resets at ${clock(first.limitResets)}` : "";
  const tail = running.length === 1 ? "It resumes on its own." : "They resume on their own.";
  return `${subject} on ${kind}${reset}. ${tail}`;
}

// The plain reason of an owner's park: its text with the first letter capitalised and a final full stop.
function plainReason(reason: string): string {
  const capital = reason.charAt(0).toUpperCase() + reason.slice(1);
  return capital.endsWith(".") ? capital : `${capital}.`;
}

// What asks the owner, first match: the open question its steerer does not hold, the owner's park, the planner's park as
// its quiet line, the steerer's park as its quiet line, the first question the steerer holds as its quiet line, or
// every running step limited. Null when nothing asks.
export function askingOf(detail: EpicsGetResponse): Asking {
  const question = ownerQuestions(detail)[0];
  if (question !== undefined) {
    return { kind: "question", question };
  }
  if (ownerParked(detail)) {
    return { kind: "park", reason: plainReason(detail.parked) };
  }
  if (plannerParked(detail)) {
    return { kind: "planner", line: `The planner holds this park: ${detail.parked}. It will retry, replan, or ask you.` };
  }
  if (steererParked(detail)) {
    return { kind: "steerer", line: `Its steerer holds this park, and answers it or escalates it to you: ${detail.parked}` };
  }
  const steerer = steererQuestions(detail)[0];
  if (steerer !== undefined) {
    return { kind: "steerer", line: steererQuestionLine(steerer) };
  }
  if (allLimited(detail)) {
    return { kind: "limited" };
  }
  return null;
}

// The running steps as Now's rows, one each.
export function stepLinesOf(detail: EpicsGetResponse): StepLine[] {
  return runningSteps(detail).map((step) => ({
    id: step.id,
    glyph: step.held ? "held" : step.limited ? "limited" : "working",
    label: stepName(step),
    task: step.task,
    doing: rowDoing(step),
    since: Number(step.stageStarted > 0n ? step.stageStarted : step.started),
  }));
}

// What a running step's row says it does: its limit or hold while it waits, else what it is doing.
function rowDoing(step: EpicStep): string {
  if (step.held) {
    return step.reason === "" ? "held" : `held · ${step.reason}`;
  }
  if (step.limited) {
    const waits = step.limitKind === "" ? "waits on a usage limit" : `waits on the ${step.limitKind} limit`;
    return step.limitResets > 0n ? `${waits} · resets ${clock(step.limitResets)}` : waits;
  }
  return step.doing;
}

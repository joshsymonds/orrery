export type QuestionOption = { label: string; description: string };

export type ParsedQuestion = { title: string; options: QuestionOption[] };

type JsonObject = { [key: string]: unknown };

const permissionOptions: QuestionOption[] = [
  { label: "1 Yes", description: "" },
  { label: "2 Yes, don't ask again", description: "" },
  { label: "3 No", description: "" },
];

// parseQuestion reads a pane's stored @orrery_question the way the phone does. An AskUserQuestion value
// gives its first question's text and one option per entry. A tool permission value gives "tool: detail"
// and the three permission options. Anything else, including text that is not a JSON object, is its own title.
export function parseQuestion(stored: string): ParsedQuestion {
  const value = parseObject(stored);
  if (value === undefined) {
    return { title: stored, options: permissionOptions };
  }

  const questions = value.questions;
  if (Array.isArray(questions)) {
    const first = asObject(questions[0]);
    if (first !== undefined) {
      return {
        title: asString(first.question),
        options: Array.isArray(first.options) ? first.options.map(asOption) : [],
      };
    }
  }

  const tool = asString(value.tool);
  const detail = permissionDetail(asObject(value.input));
  return { title: detail === "" ? tool : `${tool}: ${detail}`, options: permissionOptions };
}

function parseObject(stored: string): JsonObject | undefined {
  try {
    return asObject(JSON.parse(stored));
  } catch {
    return undefined;
  }
}

function asObject(value: unknown): JsonObject | undefined {
  if (typeof value === "object" && value !== null && !Array.isArray(value)) {
    return value as JsonObject;
  }
  return undefined;
}

function asString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function asOption(entry: unknown): QuestionOption {
  const option = asObject(entry);
  return {
    label: asString(option?.label),
    description: asString(option?.description),
  };
}

function permissionDetail(input: JsonObject | undefined): string {
  for (const key of ["command", "file_path", "url"]) {
    const value = asString(input?.[key]);
    if (value !== "") {
      return value;
    }
  }
  return "";
}

// QuestionShape is the shape of a pane's stored question: an AskUserQuestion value (askUser, with its question count),
// a tool permission value (permission, with its tool and detail), or anything else (text). count is the number of
// questions an AskUserQuestion value asks, and 1 for the other shapes.
export interface QuestionShape {
  kind: "askUser" | "permission" | "text";
  tool: string;
  detail: string;
  count: number;
}

// readQuestion reads a pane's stored @orrery_question as the shape its Ask draws. It follows the rule parseQuestion
// reads the value by: text that is not a JSON object, and an object with no tool or question, is text.
export function readQuestion(stored: string): QuestionShape {
  const text: QuestionShape = { kind: "text", tool: "", detail: "", count: 1 };
  const value = parseObject(stored);
  if (value === undefined) {
    return text;
  }

  const questions = value.questions;
  if (Array.isArray(questions) && asObject(questions[0]) !== undefined) {
    return { kind: "askUser", tool: "", detail: "", count: questions.length };
  }

  const tool = asString(value.tool);
  if (tool === "") {
    return text;
  }
  return { kind: "permission", tool, detail: permissionDetail(asObject(value.input)), count: 1 };
}

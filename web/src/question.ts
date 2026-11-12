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

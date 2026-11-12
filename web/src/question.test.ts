import assert from "node:assert/strict";
import { test } from "node:test";

import { parseQuestion } from "./question";

const permissionOptions = [
  { label: "1 Yes", description: "" },
  { label: "2 Yes, don't ask again", description: "" },
  { label: "3 No", description: "" },
];

test("an AskUserQuestion value gives its first question's text and one option per entry", () => {
  const stored = JSON.stringify({
    questions: [
      {
        question: "Which database should the sample use?",
        options: [
          { label: "SQLite", description: "Embedded, one file" },
          { label: "Postgres", description: "" },
        ],
      },
      { question: "A second question", options: [{ label: "Ignored", description: "" }] },
    ],
  });

  const parsed = parseQuestion(stored);

  assert.equal(parsed.title, "Which database should the sample use?");
  assert.deepEqual(parsed.options, [
    { label: "SQLite", description: "Embedded, one file" },
    { label: "Postgres", description: "" },
  ]);
  assert.ok(!parsed.title.includes("{"), "title must not carry raw JSON");
  assert.ok(!parsed.title.includes('"questions"'), "title must not carry raw JSON");
});

test("an AskUserQuestion option without a description has an empty description", () => {
  const stored = JSON.stringify({
    questions: [{ question: "Pick one", options: [{ label: "Only" }] }],
  });

  assert.deepEqual(parseQuestion(stored), {
    title: "Pick one",
    options: [{ label: "Only", description: "" }],
  });
});

test("a Bash permission value gives tool and command as its title and the three permission options", () => {
  const stored = JSON.stringify({ tool: "Bash", input: { command: "make test" } });

  assert.deepEqual(parseQuestion(stored), {
    title: "Bash: make test",
    options: permissionOptions,
  });
});

test("a permission value detail is file_path or url when there is no command", () => {
  const edit = JSON.stringify({ tool: "Edit", input: { file_path: "/work/app/main.go" } });
  const fetch = JSON.stringify({ tool: "WebFetch", input: { url: "https://example.com/docs" } });

  assert.equal(parseQuestion(edit).title, "Edit: /work/app/main.go");
  assert.equal(parseQuestion(fetch).title, "WebFetch: https://example.com/docs");
});

test("a permission value with no usable detail gives just the tool", () => {
  const stored = JSON.stringify({ tool: "Grep", input: { pattern: "todo" } });

  assert.deepEqual(parseQuestion(stored), { title: "Grep", options: permissionOptions });
});

test("plain text gives itself as the title with the three permission options", () => {
  assert.deepEqual(parseQuestion("Proceed with the rename?"), {
    title: "Proceed with the rename?",
    options: permissionOptions,
  });
});

test("malformed JSON gives itself as the title with the three permission options", () => {
  const stored = '{"tool": "Bash", "input": {';

  assert.deepEqual(parseQuestion(stored), { title: stored, options: permissionOptions });
});

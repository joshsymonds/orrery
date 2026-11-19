import assert from "node:assert/strict";
import { test } from "node:test";

import { parseShootArgs, resolveNames } from "./args";

test("parseShootArgs: a component/state and a bare component are names, and the width is 1600 when none is given", () => {
  assert.deepEqual(parseShootArgs(["app/attention", "inbox-row"]), {
    names: ["app/attention", "inbox-row"],
    widths: [1600],
  });
});

test("parseShootArgs: each --width is a width, in the order given, and a repeated width is kept once", () => {
  assert.deepEqual(parseShootArgs(["page/list-home", "--width", "1000", "--width", "412", "--width", "1000"]), {
    names: ["page/list-home"],
    widths: [1000, 412],
  });
});

test("parseShootArgs: no name, a missing, zero, negative or non-integer width, and an unknown option each fail with the usage", () => {
  const bad = [
    [],
    ["--width", "412"],
    ["app/attention", "--width"],
    ["app/attention", "--width", "0"],
    ["app/attention", "--width", "-1"],
    ["app/attention", "--width", "1.5"],
    ["app/attention", "--width", "wide"],
    ["app/attention", "--width", "--width"],
    ["app/attention", "--height", "900"],
  ];
  for (const argv of bad) {
    const parsed = parseShootArgs(argv);
    assert.ok("error" in parsed, `parseShootArgs(${JSON.stringify(argv)}) reads no error`);
    assert.ok(parsed.error.includes("just shoot NAME... [--width W]..."), `the error for ${JSON.stringify(argv)} carries no usage`);
  }
});

test("resolveNames: a component/state names that story, a bare component names all its states, and a name twice is shot once", () => {
  const available = ["app/attention", "inbox-row/done", "inbox-row/waiting", "page/list-home"];
  assert.deepEqual(resolveNames(["app/attention"], available), { names: ["app/attention"] });
  assert.deepEqual(resolveNames(["inbox-row"], available), { names: ["inbox-row/done", "inbox-row/waiting"] });
  assert.deepEqual(resolveNames(["inbox-row/done", "inbox-row"], available), {
    names: ["inbox-row/done", "inbox-row/waiting"],
  });
});

test("resolveNames: a name the catalog does not hold fails, naming it, whether bare or with a state", () => {
  const available = ["app/attention", "inbox-row/waiting"];
  const bare = resolveNames(["nosuch"], available);
  assert.ok("error" in bare, "a bare name the catalog lacks resolves");
  assert.ok(bare.error.includes("nosuch"), "the failure does not name the bare name");
  const withState = resolveNames(["app/nosuch"], available);
  assert.ok("error" in withState, "a component/state the catalog lacks resolves");
  assert.ok(withState.error.includes("app/nosuch"), "the failure does not name the component/state");
});

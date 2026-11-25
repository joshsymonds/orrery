import assert from "node:assert/strict";
import { test } from "node:test";

import { Code, ConnectError } from "@connectrpc/connect";

import { couldNotTell, hashOf, nothingChanged, runWrite, usedBy } from "./accountswrite";

test("AccountsWrite: a write that answers is done with its answer", async () => {
  assert.deepEqual(await runWrite(async () => "max-1"), { kind: "done", answer: "max-1" });
});

test("AccountsWrite: an InvalidArgument, NotFound or FailedPrecondition error is refused with its code and message", async () => {
  const refusals: [Code, string][] = [
    [Code.InvalidArgument, "name: empty"],
    [Code.NotFound, "account: unknown id"],
    [Code.FailedPrecondition, "account: in use by context personal"],
  ];
  for (const [code, message] of refusals) {
    const outcome = await runWrite(async () => {
      throw new ConnectError(message, code);
    });
    assert.deepEqual(outcome, { kind: "refused", code, reason: message }, message);
  }
});

test("AccountsWrite: any other error is unknown, which the page shows as could not tell, whatever its message", async () => {
  const failures: (() => never)[] = [
    () => {
      throw new ConnectError("deadline exceeded", Code.DeadlineExceeded);
    },
    () => {
      throw new ConnectError("account store failed", Code.Internal);
    },
    () => {
      throw new ConnectError("the server did not answer", Code.Unavailable);
    },
    () => {
      throw new Error("socket closed");
    },
  ];
  for (const failure of failures) {
    assert.deepEqual(await runWrite(async () => failure()), { kind: "unknown" });
  }
});

test("AccountsWrite: a write whose call throws before it returns a promise is unknown", async () => {
  const outcome = await runWrite((): Promise<string> => {
    throw new ConnectError("no answer", Code.Unavailable);
  });
  assert.deepEqual(outcome, { kind: "unknown" });
});

test("AccountsWrite: the two fixed texts are the ones a refused write and a write with no answer show", () => {
  assert.equal(nothingChanged, "Nothing changed.");
  assert.equal(couldNotTell, "Could not tell whether that landed: no answer came. This is what stands now.");
});

test("AccountsWrite: usedBy lists the contexts on an account, in registry order, and none for an id no context names", () => {
  const registry = {
    contexts: [
      { id: "personal", account: "max-1" },
      { id: "work", account: "" },
      { id: "side", account: "max-1" },
      { id: "lab", account: "pro-1" },
    ],
  };
  assert.deepEqual(usedBy(registry, "max-1"), ["personal", "side"]);
  assert.deepEqual(usedBy(registry, "pro-1"), ["lab"]);
  assert.deepEqual(usedBy(registry, "max-9"), []);
});

test("AccountsWrite: usedBy names no context for the empty id, though some contexts have no account", () => {
  const registry = { contexts: [{ id: "work", account: "" }] };
  assert.deepEqual(usedBy(registry, ""), []);
});

test("AccountsWrite: hashOf is # and the first six characters of the fingerprint", () => {
  assert.equal(hashOf("3f9a1c0e7b25"), "#3f9a1c");
  assert.equal(hashOf("b0217e"), "#b0217e");
});

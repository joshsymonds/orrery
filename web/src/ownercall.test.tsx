import assert from "node:assert/strict";
import { test, type TestContext } from "node:test";

import { Code, ConnectError } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import { type CallOutcome, type OwnerCall, sendOnce, useOwnerCall } from "./ownercall";
import { installTestDom, removeTestDom } from "./testdom";
import { turn, until } from "./testwait";

// Deferred is a promise its test settles by hand, so a call or a read is held open until the test answers it.
interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (error: Error) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve: (value: T) => void = () => undefined;
  let reject: (error: Error) => void = () => undefined;
  const promise = new Promise<T>((settle, fail) => {
    resolve = settle;
    reject = fail;
  });
  return { promise, resolve, reject };
}

// Sender records each send the call makes, and holds each open until its test answers it.
interface Sender {
  send: () => Promise<CallOutcome>;
  answers: Deferred<CallOutcome>[];
}

function sender(): Sender {
  const answers: Deferred<CallOutcome>[] = [];
  return {
    answers,
    send: () => {
      const answer = deferred<CallOutcome>();
      answers.push(answer);
      return answer.promise;
    },
  };
}

// Probe holds the call the harness drew last, so a test reads what its calls changed.
interface Probe {
  call: OwnerCall | null;
}

// Harness holds useOwnerCall over reread and draws nothing: a test reads the call from the probe.
function Harness({ reread, probe }: { reread: () => Promise<boolean>; probe: Probe }) {
  probe.call = useOwnerCall(reread);
  return null;
}

// Rig is what a check reaches: the call the harness drew last, and the reads the call started, each held open until its
// test answers it.
interface Rig {
  call: () => OwnerCall;
  reads: Deferred<boolean>[];
}

// withCall draws the harness into a fresh test DOM, runs the check on it, and removes the DOM however the check ends.
async function withCall(check: (rig: Rig) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  const probe: Probe = { call: null };
  const reads: Deferred<boolean>[] = [];
  const reread = () => {
    const read = deferred<boolean>();
    reads.push(read);
    return read.promise;
  };
  try {
    await act(() => {
      render(<Harness reread={reread} probe={probe} />, container);
    });
    await check({
      call: () => {
        assert.ok(probe.call !== null, "the harness drew no call");
        return probe.call;
      },
      reads,
    });
  } finally {
    await act(() => {
      render(null, container);
    });
    await removeTestDom(window);
  }
}

// run makes one call's step inside act, so the render it causes is applied.
async function run(rig: Rig, step: (call: OwnerCall) => void): Promise<void> {
  await act(() => {
    step(rig.call());
  });
}

// settle waits until check holds, each round running inside act, so the renders an answer causes are applied first.
function settle(t: TestContext, check: () => boolean, what: string): Promise<void> {
  return until(t.signal, check, what, { step: () => act(turn) });
}

test("OwnerCall: a confirm sends once, and a second confirm or an ask while sending sends nothing", (t) =>
  withCall(async (rig) => {
    const sent = sender();
    await run(rig, (call) => call.ask("release"));
    await run(rig, (call) => call.confirm(sent.send));
    await run(rig, (call) => call.confirm(sent.send));
    await run(rig, (call) => call.ask("close"));
    await run(rig, (call) => call.keep());
    assert.equal(sent.answers.length, 1);
    const call = rig.call();
    assert.equal(call.phase, "sending");
    assert.equal(call.asking, "release");
    assert.equal(call.line, "Sending…");
    assert.equal(call.disabled, true);
    assert.equal(call.refused, false);

    sent.answers[0].resolve({ kind: "refused", reason: "the decision is still open" });
    await settle(t, () => rig.call().phase === "rest", "the refusal is shown");
  }));

test("OwnerCall: keep returns asking to rest and sends nothing", () =>
  withCall(async (rig) => {
    const sent = sender();
    await run(rig, (call) => call.ask("release"));
    await run(rig, (call) => call.keep());
    const call = rig.call();
    assert.equal(call.phase, "rest");
    assert.equal(call.asking, "");
    assert.equal(call.line, "");
    assert.equal(call.disabled, false);
    await run(rig, (call) => call.confirm(sent.send));
    assert.equal(sent.answers.length, 0);
    assert.equal(rig.call().phase, "rest");
    assert.equal(rig.reads.length, 0);
  }));

test("OwnerCall: a landed call reads the epic again, disabled until the read answers, then rests with no line", (t) =>
  withCall(async (rig) => {
    const sent = sender();
    await run(rig, (call) => call.ask("release"));
    await run(rig, (call) => call.confirm(sent.send));
    sent.answers[0].resolve({ kind: "landed" });
    await settle(t, () => rig.call().phase === "reading", "the epic is read again");
    const reading = rig.call();
    assert.equal(reading.line, "Sent. Reading the epic again…");
    assert.equal(reading.disabled, true);
    assert.equal(rig.reads.length, 1);

    rig.reads[0].resolve(true);
    await settle(t, () => rig.call().phase === "rest", "the read answers");
    const rest = rig.call();
    assert.equal(rest.line, "");
    assert.equal(rest.disabled, false);
    assert.equal(rest.refused, false);
    assert.equal(rig.reads.length, 1);
  }));

test("OwnerCall: a landed call whose read fails is unread with its line, and readAgain reads until it answers", (t) =>
  withCall(async (rig) => {
    const sent = sender();
    await run(rig, (call) => call.ask("release"));
    await run(rig, (call) => call.confirm(sent.send));
    sent.answers[0].resolve({ kind: "landed" });
    await settle(t, () => rig.call().phase === "reading", "the epic is read again");
    rig.reads[0].resolve(false);
    await settle(t, () => rig.call().phase === "unread", "the failed read is shown");
    const unread = rig.call();
    assert.equal(unread.line, "Sent. The epic could not be read again.");
    assert.equal(unread.disabled, true);
    assert.equal(rig.reads.length, 1);

    await run(rig, (call) => call.readAgain());
    assert.equal(rig.call().phase, "reading");
    assert.equal(rig.call().line, "Sent. Reading the epic again…");
    assert.equal(rig.reads.length, 2);
    await run(rig, (call) => call.readAgain());
    assert.equal(rig.reads.length, 2);

    rig.reads[1].resolve(true);
    await settle(t, () => rig.call().phase === "rest", "the read answers");
    assert.equal(rig.call().line, "");
  }));

test("OwnerCall: an unanswered call shows its reading line, stays disabled, reads again, then rests with its line", (t) =>
  withCall(async (rig) => {
    const sent = sender();
    await run(rig, (call) => call.ask("release"));
    await run(rig, (call) => call.confirm(sent.send));
    sent.answers[0].resolve({ kind: "unanswered" });
    await settle(t, () => rig.call().phase === "reading", "the epic is read again");
    const reading = rig.call();
    assert.equal(reading.line, "No answer came: it may have gone through. Reading the epic again…");
    assert.equal(reading.disabled, true);
    assert.equal(rig.reads.length, 1);

    rig.reads[0].resolve(true);
    await settle(t, () => rig.call().phase === "rest", "the read answers");
    const rest = rig.call();
    assert.equal(rest.line, "No answer came: it may have gone through.");
    assert.equal(rest.disabled, false);
    assert.equal(rest.refused, false);
  }));

test("OwnerCall: an unanswered call whose read fails is unread with its line, and readAgain reads until it answers", (t) =>
  withCall(async (rig) => {
    const sent = sender();
    await run(rig, (call) => call.ask("release"));
    await run(rig, (call) => call.confirm(sent.send));
    sent.answers[0].resolve({ kind: "unanswered" });
    await settle(t, () => rig.call().phase === "reading", "the epic is read again");
    rig.reads[0].resolve(false);
    await settle(t, () => rig.call().phase === "unread", "the failed read is shown");
    const unread = rig.call();
    assert.equal(unread.line, "No answer came: it may have gone through. The epic could not be read again.");
    assert.equal(unread.disabled, true);

    await run(rig, (call) => call.readAgain());
    assert.equal(rig.reads.length, 2);
    rig.reads[1].resolve(true);
    await settle(t, () => rig.call().phase === "rest", "the read answers");
    assert.equal(rig.call().line, "No answer came: it may have gone through.");
  }));

test("OwnerCall: a refused call rests at once with its reason, enabled again, and a second send is confirmed again", (t) =>
  withCall(async (rig) => {
    const sent = sender();
    await run(rig, (call) => call.ask("release"));
    await run(rig, (call) => call.confirm(sent.send));
    sent.answers[0].resolve({ kind: "refused", reason: "the decision is still open" });
    await settle(t, () => rig.call().phase === "rest", "the refusal is shown");
    const refused = rig.call();
    assert.equal(refused.line, "Refused: the decision is still open");
    assert.equal(refused.refused, true);
    assert.equal(refused.disabled, false);
    assert.equal(rig.reads.length, 0);

    await run(rig, (call) => call.ask("release"));
    assert.equal(rig.call().phase, "asking");
    assert.equal(rig.call().line, "Refused: the decision is still open");
    assert.equal(rig.call().refused, true);
    await run(rig, (call) => call.confirm(sent.send));
    assert.equal(sent.answers.length, 2);
    assert.equal(rig.call().line, "Sending…");
    assert.equal(rig.call().refused, false);
  }));

test("OwnerCall: a send that rejects reads as unanswered: its reading line, disabled, and one read", (t) =>
  withCall(async (rig) => {
    const sent = sender();
    await run(rig, (call) => call.ask("release"));
    await run(rig, (call) => call.confirm(sent.send));
    sent.answers[0].reject(new Error("the send threw"));
    await settle(t, () => rig.call().phase === "reading", "the epic is read again");
    assert.equal(rig.call().line, "No answer came: it may have gone through. Reading the epic again…");
    assert.equal(rig.call().disabled, true);
    assert.equal(rig.reads.length, 1);
    assert.equal(sent.answers.length, 1);
  }));

test("OwnerCall: a read that rejects is a failed read: unread with its line", (t) =>
  withCall(async (rig) => {
    const sent = sender();
    await run(rig, (call) => call.ask("release"));
    await run(rig, (call) => call.confirm(sent.send));
    sent.answers[0].resolve({ kind: "landed" });
    await settle(t, () => rig.call().phase === "reading", "the epic is read again");
    rig.reads[0].reject(new Error("the read threw"));
    await settle(t, () => rig.call().phase === "unread", "the failed read is shown");
    assert.equal(rig.call().line, "Sent. The epic could not be read again.");
    assert.equal(rig.call().disabled, true);
  }));

test("OwnerCall: sendOnce resolves landed on an answer, unanswered on Unavailable, DeadlineExceeded or Unknown, and a refusal otherwise", async () => {
  assert.deepEqual(await sendOnce(() => Promise.resolve("")), { kind: "landed" });
  for (const code of [Code.Unavailable, Code.DeadlineExceeded, Code.Unknown]) {
    const outcome = await sendOnce(() => Promise.reject(new ConnectError("no answer", code)));
    assert.deepEqual(outcome, { kind: "unanswered" }, `code ${code}`);
  }
  const refused = await sendOnce(() =>
    Promise.reject(new ConnectError("the decision is still open", Code.FailedPrecondition)),
  );
  assert.deepEqual(refused, { kind: "refused", reason: "the decision is still open" });
  const empty = await sendOnce(() => Promise.reject(new ConnectError("", Code.Aborted)));
  assert.deepEqual(empty, { kind: "refused", reason: "no reason was given" });
});

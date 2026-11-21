import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";

import { epicCommands, newerDetail } from "./app";
import { createClients } from "./client";
import type { EpicCommands } from "./epicpage";
import {
  Epics,
  EpicSchema,
  EpicsCloseResponseSchema,
  EpicsGetResponseSchema,
  EpicsReleaseResponseSchema,
} from "./gen/orrery/v1/epics_pb";

// Served is what a test drives: the commands bound to an epic over a router that answers Release and Close, and the
// request each call reached the server with, in order.
interface Served {
  releases: { id: string; undoNone: number[] }[];
  closes: { id: string; kind: string; by: string }[];
  commands: (epicId: string) => EpicCommands;
}

// served makes the epic commands over a router. A call named in failing is answered with its error, and is recorded
// before the error is thrown.
function served(failing: { release?: ConnectError; close?: ConnectError } = {}): Served {
  const releases: Served["releases"] = [];
  const closes: Served["closes"] = [];
  const transport = createRouterTransport((router) => {
    router.service(Epics, {
      release: (request) => {
        releases.push({ id: request.id, undoNone: [...request.undoNone] });
        if (failing.release !== undefined) {
          throw failing.release;
        }
        return create(EpicsReleaseResponseSchema, {});
      },
      close: (request) => {
        closes.push({ id: request.id, kind: request.kind, by: request.by });
        if (failing.close !== undefined) {
          throw failing.close;
        }
        return create(EpicsCloseResponseSchema, {});
      },
    });
  });
  const clients = createClients(transport);
  return {
    releases,
    closes,
    commands: (epicId) => epicCommands(clients, epicId, () => undefined, async () => true),
  };
}

test("ReleaseFrom: release([2]) sends exactly one Epics.Release with the epic's id and undoNone [2]", async () => {
  const { releases, commands } = served();
  assert.deepEqual(await commands("orrery-1").release([2]), { kind: "landed" });
  assert.deepEqual(releases, [{ id: "orrery-1", undoNone: [2] }]);
});

test("ReleaseFrom: a release that gets no answer is unanswered, and is sent once, never retried", async () => {
  const { releases, commands } = served({ release: new ConnectError("orreryd is not answering", Code.Unavailable) });
  assert.deepEqual(await commands("orrery-1").release([2]), { kind: "unanswered" });
  assert.equal(releases.length, 1);
});

test("ReleaseFrom: a release the server refuses is refused with its message, and is sent once", async () => {
  const refusal = new ConnectError("the release has no undo-none step to run again", Code.FailedPrecondition);
  const { releases, commands } = served({ release: refusal });
  assert.deepEqual(await commands("orrery-1").release([]), {
    kind: "refused",
    reason: "the release has no undo-none step to run again",
  });
  assert.equal(releases.length, 1);
});

test("EpicClose: close sends exactly one Epics.Close with the epic's id, the kind and the by given", async () => {
  const { closes, commands } = served();
  assert.deepEqual(await commands("orrery-1").close("superseded", "orrery-2"), { kind: "landed" });
  assert.deepEqual(closes, [{ id: "orrery-1", kind: "superseded", by: "orrery-2" }]);
});

test("EpicClose: a close that gets no answer is unanswered, and is sent once, never retried", async () => {
  const { closes, commands } = served({ close: new ConnectError("orreryd is not answering", Code.Unavailable) });
  assert.deepEqual(await commands("orrery-1").close("abandoned", ""), { kind: "unanswered" });
  assert.equal(closes.length, 1);
});

test("EpicClose: release([]) sends one Epics.Release with undoNone empty", async () => {
  const { releases, commands } = served();
  assert.deepEqual(await commands("orrery-1").release([]), { kind: "landed" });
  assert.deepEqual(releases, [{ id: "orrery-1", undoNone: [] }]);
});

test("EpicCommands: reread is the read the binding was given, and resolves its answer", async () => {
  const transport = createRouterTransport(() => undefined);
  const commands = epicCommands(createClients(transport), "orrery-1", () => undefined, async () => false);
  assert.equal(await commands.reread(), false);
});

// epicDetail makes the epic's detail at a revision.
function epicDetail(revision: bigint) {
  return create(EpicsGetResponseSchema, { epic: create(EpicSchema, { id: "orrery-1", revision }) });
}

test("EpicCommands: newerDetail picks the asked detail when its revision is higher", () => {
  const asked = epicDetail(9n);
  assert.equal(newerDetail(epicDetail(8n), asked), asked);
});

test("EpicCommands: newerDetail picks the polled detail when its revision is higher, and on a tie", () => {
  const polled = epicDetail(9n);
  assert.equal(newerDetail(polled, epicDetail(8n)), polled);
  assert.equal(newerDetail(polled, epicDetail(9n)), polled);
});

test("EpicCommands: newerDetail picks the one detail held, and the polled detail when either has no epic", () => {
  const asked = epicDetail(3n);
  const polled = epicDetail(4n);
  const noEpic = create(EpicsGetResponseSchema, {});
  assert.equal(newerDetail(undefined, asked), asked);
  assert.equal(newerDetail(polled, undefined), polled);
  assert.equal(newerDetail(polled, noEpic), polled);
  assert.equal(newerDetail(noEpic, asked), noEpic);
});

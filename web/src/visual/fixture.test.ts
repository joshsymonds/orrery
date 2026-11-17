import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { request as httpRequest } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, test } from "node:test";

import { encodeEnvelope, type Fixture, startFixture } from "./fixture";

let dir = "";
let fixture: Fixture;

before(async () => {
  dir = await mkdtemp(join(tmpdir(), "visual-fixture-test-"));
  const bundleDir = join(dir, "bundle");
  const wireframeDir = join(dir, "wireframe");
  await mkdir(bundleDir);
  await mkdir(wireframeDir);
  await writeFile(join(bundleDir, "index.html"), '<!doctype html><div id="app"></div>');
  await writeFile(join(wireframeDir, "index.html"), "<!doctype html><title>wireframe</title>");
  fixture = await startFixture({ bundleDir, wireframeDir });
});

after(async () => {
  await fixture.close();
  await rm(dir, { recursive: true, force: true });
});

// call posts one Connect unary call, with the client header, and returns its status and JSON body.
async function call(method: string, body: unknown): Promise<{ status: number; json: unknown }> {
  const response = await fetch(`${fixture.origin}/orrery.v1.${method}`, {
    method: "POST",
    headers: { "content-type": "application/json", "x-orrery-client": "orrery web" },
    body: JSON.stringify(body),
  });
  return { status: response.status, json: await response.json() };
}

test("encodeEnvelope frames a message with flag 0 and an end-of-stream with flag 2", () => {
  const message = encodeEnvelope(0, new TextEncoder().encode('{"a":1}'));
  assert.deepEqual([...message.subarray(0, 5)], [0, 0, 0, 0, 7]);
  assert.equal(new TextDecoder().decode(message.subarray(5)), '{"a":1}');

  const end = encodeEnvelope(0x02, new TextEncoder().encode("{}"));
  assert.deepEqual([...end], [0x02, 0, 0, 0, 2, 0x7b, 0x7d]);
});

test("int64 fields go out as decimal strings", async () => {
  const { status, json } = await call("Epics/List", {});
  assert.equal(status, 200);
  const epics = (json as { epics: { id: string; changed: unknown }[] }).epics;
  const followups = epics.find((epic) => epic.id === "orrery/orrery-followups");
  assert.ok(followups, "the followups epic is listed");
  // @14:40 on 2026-11-14 UTC is 1794667200 seconds.
  assert.equal(followups.changed, "1794667200");
});

test("a GET with a malformed percent escape answers 400 and the fixture keeps serving", async () => {
  const bad = await fetch(`${fixture.origin}/%E0%A4%A`);
  assert.equal(bad.status, 400);
  await bad.arrayBuffer();
  const good = await fetch(`${fixture.origin}/index.html`);
  assert.equal(good.status, 200);
  await good.arrayBuffer();
});

test("a request target the URL parser rejects answers 400 and the fixture keeps serving", async () => {
  const status = await new Promise<number>((resolveStatus, rejectStatus) => {
    const port = Number(new URL(fixture.origin).port);
    const request = httpRequest({ host: "127.0.0.1", port, path: "http://[bad", method: "GET" }, (response) => {
      response.resume();
      resolveStatus(response.statusCode ?? 0);
    });
    request.on("error", rejectStatus);
    request.end();
  });
  assert.equal(status, 400);
  const good = await fetch(`${fixture.origin}/index.html`);
  assert.equal(good.status, 200);
  await good.arrayBuffer();
});

test("an unknown epic id answers not_found with HTTP 404", async () => {
  const { status, json } = await call("Epics/Get", { id: "nowhere/none" });
  assert.equal(status, 404);
  assert.equal((json as { code: string }).code, "not_found");
});

test("an absorbed call answers HTTP 200 with an empty body", async () => {
  const { status, json } = await call("Presence/Report", { inputAgeMs: "0" });
  assert.equal(status, 200);
  assert.deepEqual(json, {});
});

test("a server stream answers with an enveloped message and holds open", async () => {
  const request = encodeEnvelope(0, new TextEncoder().encode(JSON.stringify({ pane: "%11", limit: 30, after: "0" })));
  const response = await fetch(`${fixture.origin}/orrery.v1.Panes/WatchConversation`, {
    method: "POST",
    headers: { "content-type": "application/connect+json", "x-orrery-client": "orrery web" },
    body: new Blob([request]),
  });
  assert.equal(response.status, 200);
  assert.equal(response.headers.get("content-type"), "application/connect+json");

  const reader = response.body?.getReader();
  assert.ok(reader, "the stream has a body");
  const bytes: number[] = [];
  const fill = async (count: number) => {
    while (bytes.length < count) {
      const chunk = await reader.read();
      assert.equal(chunk.done, false, "the stream does not end after its first message");
      bytes.push(...(chunk.value ?? []));
    }
  };
  await fill(5);
  assert.equal(bytes[0], 0, "the first envelope is a message");
  const length = new DataView(Uint8Array.from(bytes.slice(1, 5)).buffer).getUint32(0, false);
  await fill(5 + length);
  const message = JSON.parse(new TextDecoder().decode(Uint8Array.from(bytes.slice(5, 5 + length)))) as {
    transcript: string;
    seq: string;
    reset: boolean;
  };
  assert.equal(message.transcript, "%11.jsonl");
  assert.equal(message.seq, "1");
  assert.equal(message.reset, true);
  await reader.cancel();
});

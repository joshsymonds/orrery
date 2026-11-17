// The fixture origin of the visual check: one node HTTP server on 127.0.0.1, on an ephemeral port. It serves the built
// web client at /, the frozen wireframe under /wireframe/round4/, and answers the Connect calls of
// web/visual/scenarios.md sections 1.3 and 1.4 from the shared world (world.ts). Every call is recorded, so the driver
// can wait for the calls a navigation makes.

import { readFile } from "node:fs/promises";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { extname, resolve, sep } from "node:path";

import { create, type DescMessage, type MessageInitShape, toJson } from "@bufbuild/protobuf";

import {
  EpicsGetResponseSchema,
  EpicsHistoryResponseSchema,
  EpicsListResponseSchema,
  EvidenceArtifactResponseSchema,
} from "../gen/orrery/v1/epics_pb";
import {
  HostStatusResponseSchema,
  ListHeadlessResponseSchema,
  ListResponseSchema,
  PanesListResponseSchema,
  SessionsWatchConversationResponseSchema,
  WatchConversationResponseSchema,
} from "../gen/orrery/v1/orrery_pb";
import { world as shippedWorld, type World } from "./world";

// wireframePrefix is the path the wireframe directory is served under.
export const wireframePrefix = "/wireframe/round4/";

// CallRecord is one Connect call the fixture received. procedure is the path without its leading slash, for example
// "orrery.v1.Epics/Get", and body is the request message as JSON.
export interface CallRecord {
  readonly procedure: string;
  readonly body: Readonly<Record<string, unknown>>;
  readonly status: number;
}

// Fixture is a started fixture server.
export interface Fixture {
  readonly origin: string;
  readonly calls: readonly CallRecord[];
  close(): Promise<void>;
}

export interface FixtureOptions {
  // The directory scripts/build-web.sh wrote: index.html, main.js, style.css and the font files.
  readonly bundleDir: string;
  // The frozen wireframe directory, web/visual/wireframe/round4.
  readonly wireframeDir: string;
  // The world to answer from; the shared world of world.ts when omitted.
  readonly world?: World;
}

// Answer is what a call's handler returns. A message answers with its JSON body; an error answers with a Connect
// error; a stream sends its messages and then holds the stream open.
export type Answer =
  | { readonly kind: "message"; readonly json: unknown }
  | { readonly kind: "error"; readonly code: string; readonly message: string }
  | { readonly kind: "stream"; readonly frames: readonly unknown[] };

// encodeEnvelope frames one Connect streaming message: a flag byte, the payload's length as a 4-byte big-endian
// integer, then the payload. The flag 0x00 marks a message and 0x02 the end of the stream.
export function encodeEnvelope(flags: number, payload: Uint8Array): Uint8Array<ArrayBuffer> {
  const framed = new Uint8Array(new ArrayBuffer(5 + payload.length));
  framed[0] = flags;
  new DataView(framed.buffer).setUint32(1, payload.length, false);
  framed.set(payload, 5);
  return framed;
}

// The HTTP status a Connect error code answers with on a unary call.
const statusOf: Record<string, number> = {
  invalid_argument: 400,
  permission_denied: 403,
  not_found: 404,
  unimplemented: 501,
};

// streamProcedures are the server-streaming calls. Their requests and responses are enveloped.
const streamProcedures = new Set([
  "orrery.v1.Panes/WatchConversation",
  "orrery.v1.Sessions/WatchConversation",
  "orrery.v1.Notices/Watch",
  "orrery.v1.Events/Watch",
]);

// absorbed are the calls section 1.4 answers with an empty body (HTTP 200, `{}`). Control and Steps are absorbed whole.
const absorbedMethods = new Set([
  "orrery.v1.Presence/Report",
  "orrery.v1.Notices/Seen",
  "orrery.v1.Panes/MarkSeen",
  "orrery.v1.Panes/SendText",
  "orrery.v1.Panes/SendKeys",
  "orrery.v1.Panes/PullQueued",
  "orrery.v1.Epics/Seen",
  "orrery.v1.Epics/Pause",
  "orrery.v1.Epics/Resume",
  "orrery.v1.Epics/Stop",
  "orrery.v1.Epics/Retry",
  "orrery.v1.Epics/Answer",
]);
const absorbedServices = ["orrery.v1.Control/", "orrery.v1.Steps/"];

function isAbsorbed(procedure: string): boolean {
  return absorbedMethods.has(procedure) || absorbedServices.some((service) => procedure.startsWith(service));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

// reply answers with init as the message of schema, in its JSON form.
function reply<Desc extends DescMessage>(schema: Desc, init: MessageInitShape<Desc>): Answer {
  return { kind: "message", json: toJson(schema, create(schema, init)) };
}

function fail(code: string, message: string): Answer {
  return { kind: "error", code, message };
}

// answerFor is the answer to one call, given its procedure and request body.
export function answerFor(procedure: string, request: Readonly<Record<string, unknown>>, world: World): Answer {
  switch (procedure) {
    case "orrery.v1.Panes/List":
      return reply(PanesListResponseSchema, world.panes);
    case "orrery.v1.Sessions/List":
      return reply(ListResponseSchema, world.sessions);
    case "orrery.v1.Sessions/ListHeadless":
      return reply(ListHeadlessResponseSchema, world.headless);
    case "orrery.v1.Epics/List":
      return reply(EpicsListResponseSchema, world.epics);
    case "orrery.v1.Epics/Get": {
      const id = text(request.id);
      const epic = world.gets.get(id);
      return epic === undefined ? fail("not_found", `epic ${id} not found`) : reply(EpicsGetResponseSchema, epic);
    }
    case "orrery.v1.Epics/History": {
      const history = world.histories.get(text(request.id));
      return history === undefined
        ? reply(EpicsHistoryResponseSchema, { entries: [], next: "" })
        : reply(EpicsHistoryResponseSchema, history);
    }
    case "orrery.v1.Host/Status":
      return reply(HostStatusResponseSchema, world.host);
    case "orrery.v1.Evidence/Artifact": {
      if (text(request.name) !== "") {
        return fail("not_found", `artifact ${text(request.name)} not found`);
      }
      // A run's stderr is empty; its log is the stdout that Evidence/Artifact returns with an empty name.
      const log = request.stderr === true ? "" : world.evidenceLogs.get(text(request.run));
      if (log === undefined) {
        return fail("not_found", `evidence run ${text(request.run)} has no log`);
      }
      return reply(EvidenceArtifactResponseSchema, { data: new TextEncoder().encode(log), mediaType: "text/plain" });
    }
    case "orrery.v1.Control/Start":
      return { kind: "message", json: { id: "fixture-new-session" } };
    case "orrery.v1.Panes/WatchConversation": {
      const pane = world.paneStreams.get(text(request.pane));
      // A reconnect, which names a transcript or a seq past 0, gets nothing and stays open.
      const reconnect = text(request.transcript) !== "" || Number(request.after ?? 0) > 0;
      const frames = pane === undefined || reconnect ? [] : [toJson(WatchConversationResponseSchema, pane)];
      return { kind: "stream", frames };
    }
    case "orrery.v1.Sessions/WatchConversation": {
      const session = world.sessionStreams.get(text(request.id));
      const reconnect = Number(request.after ?? 0) > 0;
      const frames = session === undefined || reconnect ? [] : [toJson(SessionsWatchConversationResponseSchema, session)];
      return { kind: "stream", frames };
    }
    case "orrery.v1.Notices/Watch":
    case "orrery.v1.Events/Watch":
      return { kind: "stream", frames: [] };
    default:
      if (isAbsorbed(procedure)) {
        return { kind: "message", json: {} };
      }
      return fail("unimplemented", `${procedure} is not served by the fixture`);
  }
}

// decodeRequest reads a call's body as its JSON request message. A streaming call's body is one enveloped message; a
// unary call's body is the JSON itself.
export function decodeRequest(contentType: string, body: Uint8Array): Record<string, unknown> {
  let payload = body;
  if (contentType.startsWith("application/connect+json") && body.length >= 5 && body[0] === 0) {
    const length = new DataView(body.buffer, body.byteOffset, body.byteLength).getUint32(1, false);
    if (body.length >= 5 + length) {
      payload = body.subarray(5, 5 + length);
    }
  }
  if (payload.length === 0) {
    return {};
  }
  const parsed: unknown = JSON.parse(new TextDecoder().decode(payload));
  if (!isRecord(parsed)) {
    throw new Error("the request body is not a JSON object");
  }
  return parsed;
}

const contentTypes: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json",
  ".md": "text/markdown; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".ttf": "font/ttf",
  ".woff2": "font/woff2",
};

// fileFor maps a request path to the file it names, inside the wireframe directory under its prefix or inside the
// bundle directory otherwise. A path that leaves its root names no file.
function fileFor(pathname: string, options: FixtureOptions): string | undefined {
  const inWireframe = pathname.startsWith(wireframePrefix);
  const root = resolve(inWireframe ? options.wireframeDir : options.bundleDir);
  let relative = inWireframe ? pathname.slice(wireframePrefix.length) : pathname.slice(1);
  if (relative === "" || relative.endsWith("/")) {
    relative += "index.html";
  }
  const file = resolve(root, relative);
  return file === root || file.startsWith(root + sep) ? file : undefined;
}

// serveFile answers a GET for the raw path. A percent escape that does not decode names no file and answers 400.
async function serveFile(rawPath: string, options: FixtureOptions, res: ServerResponse): Promise<void> {
  let pathname: string;
  try {
    pathname = decodeURIComponent(rawPath);
  } catch {
    res.writeHead(400).end();
    return;
  }
  const file = fileFor(pathname, options);
  if (file === undefined) {
    res.writeHead(404).end();
    return;
  }
  try {
    const body = await readFile(file);
    res.writeHead(200, { "content-type": contentTypes[extname(file)] ?? "application/octet-stream" });
    res.end(body);
  } catch {
    res.writeHead(404).end();
  }
}

function readBody(req: IncomingMessage): Promise<Buffer> {
  return new Promise((resolveBody, rejectBody) => {
    const chunks: Buffer[] = [];
    req.on("data", (chunk: Buffer) => chunks.push(chunk));
    req.on("end", () => resolveBody(Buffer.concat(chunks)));
    req.on("error", rejectBody);
  });
}

// start serves options on 127.0.0.1 at an ephemeral port.
export async function startFixture(options: FixtureOptions): Promise<Fixture> {
  const world = options.world ?? shippedWorld;
  const calls: CallRecord[] = [];
  const openStreams = new Set<ServerResponse>();

  const record = (call: CallRecord) => {
    calls.push(call);
  };

  const rpc = async (procedure: string, req: IncomingMessage, res: ServerResponse) => {
    const contentType = String(req.headers["content-type"] ?? "");
    const streaming = streamProcedures.has(procedure);
    const body = await readBody(req);
    let request: Record<string, unknown>;
    try {
      request = decodeRequest(contentType, body);
    } catch {
      request = {};
      sendError(res, streaming, "invalid_argument", "the request body is not a JSON message", record, procedure, request);
      return;
    }
    if (String(req.headers["x-orrery-client"] ?? "") === "") {
      sendError(res, streaming, "permission_denied", "the X-Orrery-Client header is required", record, procedure, request);
      return;
    }
    const answer = answerFor(procedure, request, world);
    if (answer.kind === "error") {
      sendError(res, streaming, answer.code, answer.message, record, procedure, request);
      return;
    }
    if (answer.kind === "message") {
      record({ procedure, body: request, status: 200 });
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify(answer.json));
      return;
    }
    record({ procedure, body: request, status: 200 });
    res.writeHead(200, { "content-type": "application/connect+json", "cache-control": "no-cache" });
    for (const frame of answer.frames) {
      res.write(encodeEnvelope(0, new TextEncoder().encode(JSON.stringify(frame))));
    }
    // The stream stays open: a stream holds until the fixture closes.
    openStreams.add(res);
    res.on("close", () => openStreams.delete(res));
    res.flushHeaders();
  };

  const server: Server = createServer((req, res) => {
    let pathname: string;
    try {
      pathname = new URL(req.url ?? "/", "http://fixture").pathname;
    } catch {
      res.writeHead(400).end();
      return;
    }
    if (req.method === "POST" && pathname.startsWith("/orrery.v1.")) {
      rpc(pathname.slice(1), req, res).catch((error: unknown) => {
        if (!res.headersSent) {
          res.writeHead(500, { "content-type": "application/json" });
        }
        res.end(JSON.stringify({ code: "internal", message: String(error) }));
      });
      return;
    }
    if (req.method === "GET" || req.method === "HEAD") {
      serveFile(pathname, options, res).catch(() => res.writeHead(500).end());
      return;
    }
    res.writeHead(405).end();
  });

  await new Promise<void>((resolveListen) => server.listen(0, "127.0.0.1", resolveListen));
  const address = server.address();
  if (address === null || typeof address === "string") {
    throw new Error("fixture: the server has no TCP address");
  }

  return {
    origin: `http://127.0.0.1:${address.port}`,
    calls,
    async close() {
      for (const res of openStreams) {
        res.end();
      }
      openStreams.clear();
      // Connections are destroyed before the wait: server.close resolves only once every connection has ended.
      const closed = new Promise<void>((resolveClose) => server.close(() => resolveClose()));
      server.closeAllConnections();
      await closed;
    },
  };
}

// sendError answers a call with a Connect error and records it. A stream answers with an end-of-stream envelope that
// carries the error; a unary call answers with the HTTP status and a JSON error body.
function sendError(
  res: ServerResponse,
  streaming: boolean,
  code: string,
  message: string,
  record: (call: CallRecord) => void,
  procedure: string,
  request: Record<string, unknown>,
): void {
  const status = streaming ? 200 : (statusOf[code] ?? 500);
  record({ procedure, body: request, status });
  if (streaming) {
    res.writeHead(200, { "content-type": "application/connect+json" });
    const end = new TextEncoder().encode(JSON.stringify({ error: { code, message } }));
    res.end(Buffer.from(encodeEnvelope(0x02, end)));
    return;
  }
  res.writeHead(status, { "content-type": "application/json" });
  res.end(JSON.stringify({ code, message }));
}

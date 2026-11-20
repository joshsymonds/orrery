// The in-memory fixture transport: a Connect transport that answers the web client's calls from fixture slices, with no
// network. Each read is answered from the slices, each write the client makes is absorbed, and each stream sends the
// slices' frames and then stays open until its call aborts. Every other call is refused as Unimplemented and recorded in
// unserved, so a page that makes one shows it.

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type HandlerContext, type Transport } from "@connectrpc/connect";

import { createClients } from "../client";
import { Epics, EpicsGetResponseSchema, Evidence, Steps } from "../gen/orrery/v1/epics_pb";
import { Notices } from "../gen/orrery/v1/notice_pb";
import {
  AskResponseSchema,
  Control,
  Events,
  Host,
  Panes,
  Picker,
  Presence,
  Sessions,
} from "../gen/orrery/v1/orrery_pb";
import { type Slices, shippedSlices } from "./slices";

// FixtureTransport is the fixture a page is given: the transport, the clients made from it, the procedures it refused,
// and a way to hear each refusal as it is recorded.
export type FixtureTransport = {
  readonly transport: Transport;
  readonly clients: ReturnType<typeof createClients>;
  // unserved lists the procedure of each refused call, in the order the calls came, as "orrery.v1.Picker/Ask". It grows
  // as calls come, and the fixture is the only writer.
  readonly unserved: readonly string[];
  // onUnserved registers a listener, called with the procedure each time one is added to unserved. It returns the
  // function that unregisters the listener.
  onUnserved(listener: (procedure: string) => void): () => void;
};

type Listener = (procedure: string) => void;

// createFixtureTransport returns a fixture transport that answers from slices, or from the shipped slices when it is given
// none.
export function createFixtureTransport(slices: Slices = shippedSlices): FixtureTransport {
  const unserved: string[] = [];
  const listeners = new Set<Listener>();

  // refuse records the procedure of a call the fixture does not serve, tells each listener, and fails the call.
  const refuse = (_request: unknown, context: HandlerContext): never => {
    const procedure = `${context.service.typeName}/${context.method.name}`;
    unserved.push(procedure);
    for (const listener of [...listeners]) {
      listener(procedure);
    }
    throw new ConnectError(`${procedure} is not served by the fixture`, Code.Unimplemented);
  };

  // absorb answers a write with an empty message. The fixture takes the write and records nothing.
  const absorb = () => ({});

  // epicCommand answers an epic command with an empty message, or fails it with the reason the slices name for it.
  const epicCommand = (name: "pause" | "resume" | "stop" | "retry") => () => {
    const reason = slices.failingEpicCommands.get(name);
    if (reason !== undefined) {
      throw new ConnectError(reason, Code.Unavailable);
    }
    return {};
  };

  // notFound is the error a read of an id the slices do not hold fails with.
  const notFound = (message: string) => new ConnectError(message, Code.NotFound);

  // held waits until the call's signal aborts, so a stream stays open until its client ends it.
  const held = (signal: AbortSignal) =>
    new Promise<void>((resolve) => {
      if (signal.aborted) {
        resolve();
      } else {
        signal.addEventListener("abort", () => resolve(), { once: true });
      }
    });

  // quiet is a stream that sends no frame, and stays open until its call aborts.
  async function* quiet(_request: unknown, context: HandlerContext): AsyncGenerator<never, void> {
    await held(context.signal);
  }

  const transport = createRouterTransport((router) => {
    router.service(Panes, {
      list: () => slices.panes,
      screen: refuse,
      conversation: refuse,
      sendText: absorb,
      sendKeys: absorb,
      markSeen: absorb,
      pullQueued: absorb,
      older: refuse,
      // A reconnect, which names a transcript or a seq past 0, gets no frame and stays open.
      watchConversation: async function* (request, context) {
        const pane = slices.paneStreams.get(request.pane);
        const reconnect = request.transcript !== "" || request.after > 0n;
        if (pane !== undefined && !reconnect) {
          yield pane;
        }
        await held(context.signal);
      },
    });
    router.service(Sessions, {
      list: () => slices.sessions,
      listHeadless: () => slices.headless,
      older: refuse,
      // A reconnect, which names a seq past 0, gets no frame and stays open.
      watchConversation: async function* (request, context) {
        const session = slices.sessionStreams.get(request.id);
        if (session !== undefined && request.after <= 0n) {
          yield session;
        }
        await held(context.signal);
      },
    });
    router.service(Events, { watch: quiet });
    router.service(Control, {
      start: () => ({ id: "fixture-new-session" }),
      say: absorb,
      answer: absorb,
      interrupt: absorb,
      stop: absorb,
      seen: absorb,
      resume: absorb,
      withdraw: absorb,
    });
    router.service(Picker, {
      // An ask its slices hold no answer for is refused. A held ask stays open until its call aborts.
      ask: async (request, context) => {
        const answer = slices.asks.get(request.text);
        if (answer === undefined) {
          return refuse(request, context);
        }
        if ("rows" in answer) {
          return create(AskResponseSchema, { rows: [...answer.rows] });
        }
        if ("failure" in answer) {
          throw new ConnectError(`${context.service.typeName}/${context.method.name} failed`, answer.failure);
        }
        await held(context.signal);
        return create(AskResponseSchema);
      },
      run: refuse,
    });
    router.service(Epics, {
      list: () => slices.epics,
      // A held Get stays open until its call aborts, as a Picker.Ask its slices hold open does.
      get: async (request, context) => {
        if (slices.heldGets.has(request.id)) {
          await held(context.signal);
          return create(EpicsGetResponseSchema);
        }
        const epic = slices.gets.get(request.id);
        if (epic === undefined) {
          throw notFound(`epic ${request.id} not found`);
        }
        return epic;
      },
      history: (request) => slices.histories.get(request.id) ?? { entries: [], next: "" },
      pause: epicCommand("pause"),
      resume: epicCommand("resume"),
      stop: epicCommand("stop"),
      retry: epicCommand("retry"),
      kick: refuse,
      answer: absorb,
      seen: absorb,
      archive: refuse,
      unarchive: refuse,
    });
    router.service(Evidence, {
      artifact: (request) => {
        if (request.name !== "") {
          throw notFound(`artifact ${request.name} not found`);
        }
        // A run's stderr is empty; its log is the stdout that Evidence/Artifact returns with an empty name.
        const log = request.stderr ? "" : slices.evidenceLogs.get(request.run);
        if (log === undefined) {
          throw notFound(`evidence run ${request.run} has no log`);
        }
        return { data: new TextEncoder().encode(log), mediaType: "text/plain" };
      },
    });
    router.service(Steps, { hold: absorb, nudge: absorb, stop: absorb });
    router.service(Presence, { report: absorb });
    router.service(Notices, { watch: quiet, seen: absorb });
    router.service(Host, { status: () => slices.host });
  });

  return {
    transport,
    clients: createClients(transport),
    unserved,
    onUnserved(listener) {
      // Each registration is its own function, so the same listener registered twice is called twice.
      const registered: Listener = (procedure) => listener(procedure);
      listeners.add(registered);
      return () => {
        listeners.delete(registered);
      };
    },
  };
}

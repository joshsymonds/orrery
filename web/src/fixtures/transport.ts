// The in-memory fixture transport: a Connect transport that answers the web client's calls from fixture slices, with no
// network. Each read is answered from the slices, each write the client makes is absorbed, and each stream sends the
// slices' frames and then stays open until its call aborts. Every other call is refused as Unimplemented and recorded in
// unserved, so a page that makes one shows it.

import { create } from "@bufbuild/protobuf";
import {
  Code,
  ConnectError,
  createRouterTransport,
  type HandlerContext,
  type Interceptor,
  type Transport,
} from "@connectrpc/connect";

import { createClients } from "../client";
import { Contracts } from "../gen/orrery/v1/contracts_pb";
import { Epics, EpicsContinueResponseSchema, Evidence, Steps } from "../gen/orrery/v1/epics_pb";
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
  type SessionsWatchConversationResponse,
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

  // notFound is the error a read of an id the slices do not hold fails with.
  const notFound = (message: string) => new ConnectError(message, Code.NotFound);

  // say answers Control.Say as the slices choose: absorbed when they set no say, and failed as a refusal or as a call
  // with no answer when they set one.
  const say = () => {
    if (slices.say === "refused") {
      throw new ConnectError("the session is not taking messages", Code.FailedPrecondition);
    }
    if (slices.say === "unanswered") {
      throw new ConnectError("no answer", Code.Unavailable);
    }
    return {};
  };

  // held waits until the call's signal aborts, so a stream stays open until its client ends it.
  const held = (signal: AbortSignal) =>
    new Promise<void>((resolve) => {
      if (signal.aborted) {
        resolve();
      } else {
        signal.addEventListener("abort", () => resolve(), { once: true });
      }
    });

  // gate is a client-side interceptor on every call: a procedure the slices name in fail answers Unavailable, and one they
  // name in hold stays unanswered until its call aborts. Every other call goes on to the router.
  const gate: Interceptor = (next) => async (request) => {
    const procedure = `${request.service.typeName}/${request.method.name}`;
    if (slices.fail?.has(procedure)) {
      throw new ConnectError(`${procedure} failed`, Code.Unavailable);
    }
    if (slices.hold?.has(procedure)) {
      await held(request.signal);
    }
    return next(request);
  };

  // wakers holds, for each session id, the wake of each open conversation stream of that session. A Control.Interrupt of
  // the session calls each one with the slices' then frame.
  const wakers = new Map<string, Set<(frame?: SessionsWatchConversationResponse) => void>>();

  // interrupted holds an open conversation stream of session id until its call aborts or an interrupt of the session
  // wakes it. It resolves with the frame the interrupt sends, or with undefined once the call aborts.
  function interrupted(id: string, signal: AbortSignal): Promise<SessionsWatchConversationResponse | undefined> {
    return new Promise((resolve) => {
      if (signal.aborted) {
        resolve(undefined);
        return;
      }
      const waiting = wakers.get(id) ?? new Set<(frame?: SessionsWatchConversationResponse) => void>();
      wakers.set(id, waiting);
      const finish = (frame?: SessionsWatchConversationResponse) => {
        waiting.delete(finish);
        signal.removeEventListener("abort", aborted);
        resolve(frame);
      };
      const aborted = () => finish();
      waiting.add(finish);
      signal.addEventListener("abort", aborted, { once: true });
    });
  }

  // interrupt answers Control.Interrupt as the slices choose: absorbed when they set no interrupt, and otherwise answered
  // with their texts, with their then frame sent to each open conversation stream of the session when they set one.
  const interrupt = (id: string) => {
    const answer = slices.interrupt;
    if (answer === undefined) {
      return {};
    }
    if (answer.then !== undefined) {
      for (const wake of [...(wakers.get(id) ?? [])]) {
        wake(answer.then);
      }
    }
    return { texts: answer.texts };
  };

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
      // A reconnect, which names a seq past 0, gets no frame and stays open. Each interrupt of the session sends its then
      // frame, if the slices give one, and the stream stays open until its call aborts.
      watchConversation: async function* (request, context) {
        const session = slices.sessionStreams.get(request.id);
        if (session !== undefined && request.after <= 0n) {
          yield session;
        }
        let frame = await interrupted(request.id, context.signal);
        while (frame !== undefined) {
          yield frame;
          frame = await interrupted(request.id, context.signal);
        }
      },
    });
    router.service(Contracts, {
      list: () => slices.contracts,
      get: (request) => {
        if (!Object.hasOwn(slices.contractGets, request.id)) {
          throw notFound(`contract ${request.id} not found`);
        }
        return slices.contractGets[request.id];
      },
      accept: refuse,
      setAside: refuse,
      reopen: refuse,
    });
    router.service(Events, { watch: quiet });
    router.service(Control, {
      start: () => ({ id: "fixture-new-session" }),
      say,
      answer: absorb,
      interrupt: (request) => interrupt(request.id),
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
      get: (request) => {
        const epic = slices.gets.get(request.id);
        if (epic === undefined) {
          throw notFound(`epic ${request.id} not found`);
        }
        return epic;
      },
      history: (request) => slices.histories.get(request.id) ?? { entries: [], next: "" },
      pause: absorb,
      // Continue starts no conversation in the fixture, so it answers an empty session.
      continue: () => create(EpicsContinueResponseSchema, { session: "" }),
      move: absorb,
      resume: absorb,
      stop: absorb,
      retry: absorb,
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
  }, { transport: { interceptors: [gate] } });

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

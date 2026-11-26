import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { render } from "preact";
import { act } from "preact/test-utils";

import type { Clients } from "./actions";
import { createClients } from "./client";
import {
  Control,
  Picker,
  ProposeResponseSchema,
  type StartRequest,
  StartResponseSchema,
} from "./gen/orrery/v1/orrery_pb";
import { Starter } from "./starter";
import { assertNone, installTestDom, removeTestDom, teardownOnce } from "./testdom";
import { turn, until } from "./testwait";

// Synthetic places only: invented hosts and folders. brine is the host that does not answer, so it is not reachable.
const reachable = ["halyard", "dock"];
const api = { host: "halyard", dir: "/work/api" };
const web = { host: "dock", dir: "/work/web" };
const unreachable = { host: "brine", dir: "/work/ops" };
const prompt = "fix the flaky api test";
const startFailure = "The session did not start.";
const proposeFailure = "The proposals did not come back. Your prompt is kept.";

// Place is one ranked host and folder the fake Picker.Propose answers with.
interface Place {
  host: string;
  dir: string;
}

// FakeServices answers Picker.Propose and Control.Start through a router transport. Propose answers with the places a test
// sets, or fails with the error it sets, and a Propose a test holds waits for the release holdPropose returns. Start answers
// with a session id, or fails with the error it sets. Every call is recorded.
class FakeServices {
  readonly proposeCalls: string[] = [];
  readonly startCalls: StartRequest[] = [];
  places: Place[] = [];
  proposeError: ConnectError | null = null;
  startError: ConnectError | null = null;
  private gate: Promise<void> | null = null;

  readonly clients: Clients = createClients(
    createRouterTransport((router) => {
      router.service(Picker, {
        propose: async (request) => {
          this.proposeCalls.push(request.text);
          const gate = this.gate;
          this.gate = null;
          if (gate !== null) {
            await gate;
          }
          if (this.proposeError !== null) {
            throw this.proposeError;
          }
          return create(ProposeResponseSchema, { proposals: this.places.map((place) => ({ ...place })) });
        },
      });
      router.service(Control, {
        start: async (request) => {
          this.startCalls.push(request);
          if (this.startError !== null) {
            throw this.startError;
          }
          return create(StartResponseSchema, { id: "sess-new" });
        },
      });
    }),
  );

  // holdPropose makes the next Propose wait until the release it returns is called.
  holdPropose(): () => void {
    let release = () => {};
    this.gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    return release;
  }
}

// Opening is what the Starter is opened with: a folder and a host given, whether it is a sheet, and what it reports.
interface Opening {
  folder?: string;
  host?: string;
  sheet?: boolean;
  onStarted?: (session: string) => void;
  onClose?: () => void;
}

// withStarter opens the Starter in a fresh DOM over a fake server. It is unmounted and its DOM removed when the test ends,
// or as soon as signal aborts if it times out first.
async function withStarter(
  signal: AbortSignal,
  opening: Opening,
  check: (container: HTMLElement, services: FakeServices) => Promise<void>,
): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  const teardown = teardownOnce(signal, async () => {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  });
  const services = new FakeServices();
  try {
    await act(() => {
      render(
        <Starter
          clients={services.clients}
          hosts={reachable}
          folder={opening.folder}
          host={opening.host}
          sheet={opening.sheet ?? false}
          onStarted={opening.onStarted ?? (() => {})}
          onClose={opening.onClose ?? (() => {})}
        />,
        container,
      );
    });
    await check(container, services);
  } finally {
    await teardown();
  }
}

// untilTurn waits for check one setImmediate turn at a time inside act, so the renders each turn lets through are applied.
// It reads no clock and arms no timer.
function untilTurn(signal: AbortSignal, check: () => boolean, what: string): Promise<void> {
  return until(signal, check, what, { step: () => act(turn) });
}

function promptBox(container: HTMLElement): HTMLInputElement {
  const box = container.querySelector<HTMLInputElement>('input[aria-label="Prompt"]');
  assert.ok(box, "the Starter has its prompt field");
  return box;
}

function folderBox(container: HTMLElement): HTMLInputElement {
  const box = container.querySelector<HTMLInputElement>('input[aria-label="Folder"]');
  assert.ok(box, "the Starter has its folder field");
  return box;
}

function hostSelect(container: HTMLElement): HTMLSelectElement {
  const select = container.querySelector<HTMLSelectElement>('select[aria-label="Host"]');
  assert.ok(select, "the Starter has its host choice");
  return select;
}

function startButton(container: HTMLElement): HTMLButtonElement {
  const button = container.querySelector<HTMLButtonElement>("button.primary");
  assert.ok(button, "the Starter has its Start the session button");
  return button;
}

// buttonLabelled is the button whose text is label, or fails the test when none is.
function buttonLabelled(container: HTMLElement, label: string): HTMLButtonElement {
  const button = [...container.querySelectorAll<HTMLButtonElement>("button")].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  assert.ok(button, `the Starter has a ${label} button`);
  return button;
}

// optionsOf lists the proposals in the listbox, in the order drawn; none when no listbox is drawn.
function optionsOf(container: HTMLElement): HTMLButtonElement[] {
  return [...container.querySelectorAll<HTMLButtonElement>('[role="listbox"] [role="option"]')];
}

// textOf is the text the container draws.
function textOf(container: HTMLElement): string {
  return container.textContent ?? "";
}

// alertTexts lists the text of each line in the error role.
function alertTexts(container: HTMLElement): string[] {
  return [...container.querySelectorAll('[role="alert"]')].map((element) => element.textContent ?? "");
}

// hostNamesOf lists the host each proposal row draws, in order.
function hostNamesOf(options: HTMLElement[]): string[] {
  return options.map((option) => option.querySelector(".host-label .name")?.textContent ?? "");
}

async function typeInto(box: HTMLInputElement, text: string): Promise<void> {
  await act(() => {
    box.value = text;
    box.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function pressKey(element: Element, key: string, init: KeyboardEventInit = {}): Promise<void> {
  await act(() => {
    element.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init }));
  });
}

async function click(element: HTMLElement): Promise<void> {
  await act(() => {
    element.dispatchEvent(new Event("click", { bubbles: true }));
  });
}

async function chooseHost(select: HTMLSelectElement, host: string): Promise<void> {
  await act(() => {
    select.value = host;
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

// ask types the prompt and presses ⏎ in it, which sends it to Picker.Propose.
async function ask(container: HTMLElement, text: string): Promise<void> {
  const box = promptBox(container);
  await typeInto(box, text);
  await pressKey(box, "Enter");
}

test("HostsWebStarter: Enter in the prompt calls Picker.Propose with the prompt", (t) =>
  withStarter(t.signal, {}, async (container, services) => {
    services.places = [api];
    await ask(container, prompt);
    await untilTurn(t.signal, () => optionsOf(container).length === 1, "the proposals are listed");
    assert.deepEqual(services.proposeCalls, [prompt], "Propose is called once, with the prompt as typed");
  }));

test("HostsWebStarter: the proposals draw as a listbox of start-in rows, each with its folder and its host, the top one preselected and set in the place below", (t) =>
  withStarter(t.signal, {}, async (container, services) => {
    services.places = [api, web];
    await ask(container, prompt);
    await untilTurn(t.signal, () => optionsOf(container).length === 2, "the proposals are listed");
    const options = optionsOf(container);
    assert.deepEqual(
      options.map((option) => option.querySelector(".k")?.textContent),
      ["start in", "start in"],
      "each row is a start-in row",
    );
    assert.deepEqual(
      options.map((option) => option.querySelector(".t")?.textContent),
      ["/work/api", "/work/web"],
      "each row names its folder, in the ranked order",
    );
    assert.deepEqual(hostNamesOf(options), ["halyard", "dock"], "each row draws its host");
    assert.deepEqual(
      options.map((option) => option.getAttribute("aria-selected")),
      ["true", "false"],
      "only the top proposal is preselected",
    );
    assert.ok(options[0].classList.contains("cur"), "the top proposal is the current row");
    assert.equal(hostSelect(container).value, "halyard", "the host is the top proposal's");
    assert.equal(folderBox(container).value, "/work/api", "the folder is the top proposal's");
  }));

test("HostsWebStarter: picking another proposal sets the host and the folder to it, and the list stays open", (t) =>
  withStarter(t.signal, {}, async (container, services) => {
    services.places = [api, web];
    await ask(container, prompt);
    await untilTurn(t.signal, () => optionsOf(container).length === 2, "the proposals are listed");
    await click(optionsOf(container)[1]);
    const options = optionsOf(container);
    assert.equal(options.length, 2, "the list is still open");
    assert.deepEqual(
      options.map((option) => option.getAttribute("aria-selected")),
      ["false", "true"],
      "the picked proposal is the one selected",
    );
    assert.equal(hostSelect(container).value, "dock", "the host is the picked proposal's");
    assert.equal(folderBox(container).value, "/work/web", "the folder is the picked proposal's");
  }));

test("HostsWebStarter: while Picker.Propose runs, the wait is said and the prompt is kept", (t) =>
  withStarter(t.signal, {}, async (container, services) => {
    services.places = [api];
    const release = services.holdPropose();
    await ask(container, prompt);
    await untilTurn(t.signal, () => textOf(container).includes("Finding where to run it…"), "the wait is said");
    assert.equal(promptBox(container).value, prompt, "the prompt is kept while the answer is awaited");
    assertNone(container.querySelector('[role="listbox"]'), "no proposal is listed before the answer");
    release();
    await untilTurn(t.signal, () => optionsOf(container).length === 1, "the proposals are listed");
    assert.deepEqual(services.proposeCalls, [prompt]);
  }));

test("HostsWebStarter: a failed Propose keeps the prompt, says a fixed line that never shows the server's text, and Try again asks once more", (t) =>
  withStarter(t.signal, {}, async (container, services) => {
    services.proposeError = new ConnectError("internal: the ranker stack trace", Code.Internal);
    await ask(container, prompt);
    await untilTurn(t.signal, () => alertTexts(container).length === 1, "the failure is said");
    assert.deepEqual(alertTexts(container), [proposeFailure], "the failure is the fixed line");
    assert.ok(!textOf(container).includes("ranker stack trace"), "the server's own text is not shown");
    assert.equal(promptBox(container).value, prompt, "the prompt is kept");
    services.proposeError = null;
    services.places = [api];
    await click(buttonLabelled(container, "Try again"));
    await untilTurn(t.signal, () => optionsOf(container).length === 1, "the retried proposals are listed");
    assert.deepEqual(services.proposeCalls, [prompt, prompt], "Try again sends the same prompt again");
  }));

test("HostsWebStarter: when no reachable host has a folder that fits, it says so and leaves the folder to be typed", (t) =>
  withStarter(t.signal, {}, async (container, services) => {
    services.places = [unreachable];
    await ask(container, prompt);
    await untilTurn(
      t.signal,
      () => textOf(container).includes("No reachable host has a folder that fits this."),
      "the none line is drawn",
    );
    assert.ok(
      textOf(container).includes("Choose a host and type a folder."),
      "the line asks for a host and a folder",
    );
    assertNone(container.querySelector('[role="listbox"]'), "no proposal is listed");
    assert.equal(folderBox(container).value, "", "the folder is left empty");
    assert.equal(hostSelect(container).value, "halyard", "the host choice is the first reachable host");
  }));

test("HostsWebStarter: an unreachable host is in neither the proposals nor the host choice", (t) =>
  withStarter(t.signal, {}, async (container, services) => {
    services.places = [unreachable, web, api];
    await ask(container, prompt);
    await untilTurn(t.signal, () => optionsOf(container).length === 2, "the proposals are listed");
    assert.deepEqual(hostNamesOf(optionsOf(container)), ["dock", "halyard"], "only reachable hosts are proposed");
    assert.deepEqual(
      [...hostSelect(container).querySelectorAll("option")].map((option) => option.value),
      reachable,
      "the host choice lists only the reachable hosts",
    );
    assert.equal(hostSelect(container).value, "dock", "the top reachable proposal is the one preselected");
    assert.ok(!textOf(container).includes("brine"), "the unreachable host is named nowhere");
  }));

test("HostsWebStarter: a folder typed on a chosen host closes the list, and Start sends that host and folder to Control.Start", (t) => {
  const started: string[] = [];
  return withStarter(t.signal, { onStarted: (session) => started.push(session) }, async (container, services) => {
    services.places = [api, web];
    await ask(container, prompt);
    await untilTurn(t.signal, () => optionsOf(container).length === 2, "the proposals are listed");
    await chooseHost(hostSelect(container), "dock");
    await untilTurn(t.signal, () => optionsOf(container).length === 0, "choosing a host closes the list");
    assert.ok(
      textOf(container).includes("The folder is checked on dock when the session starts."),
      "the place says the folder is checked on the chosen host",
    );
    await typeInto(folderBox(container), "/srv/app");
    assertNone(container.querySelector('[role="listbox"]'), "typing a folder keeps the list closed");
    assert.equal(hostSelect(container).value, "dock", "the chosen host stays chosen");
    assert.equal(startButton(container).disabled, false, "Start is on: the prompt has words and there is a folder");
    await click(startButton(container));
    await untilTurn(t.signal, () => started.length === 1, "the session is reported");
    assert.equal(services.startCalls.length, 1, "Control.Start is called once");
    assert.equal(services.startCalls[0].host, "dock", "Control.Start names the chosen host");
    assert.equal(services.startCalls[0].dir, "/srv/app", "Control.Start names the typed folder");
    assert.equal(services.startCalls[0].prompt, prompt, "Control.Start carries the prompt");
    assert.deepEqual(started, ["sess-new"], "the session Control.Start started is reported");
  });
});

test("HostsWebStarter: Start after a picked proposal sends its host, folder, prompt and start id to Control.Start", (t) => {
  const started: string[] = [];
  return withStarter(t.signal, { onStarted: (session) => started.push(session) }, async (container, services) => {
    services.places = [api, web];
    await ask(container, prompt);
    await untilTurn(t.signal, () => optionsOf(container).length === 2, "the proposals are listed");
    await click(optionsOf(container)[1]);
    await click(startButton(container));
    await untilTurn(t.signal, () => started.length === 1, "the session is reported");
    assert.equal(services.startCalls.length, 1, "Control.Start is called once");
    const [call] = services.startCalls;
    assert.equal(call.host, "dock", "Control.Start names the picked proposal's host");
    assert.equal(call.dir, "/work/web", "Control.Start names the picked proposal's folder");
    assert.equal(call.prompt, prompt, "Control.Start carries the prompt");
    assert.match(call.id, /^[0-9a-f]{32}$/, "Control.Start carries a start id of 32 hex digits");
  });
});

test("HostsWebStarter: a refused Start keeps everything and shows the fixed line, and the retry names the same start id", (t) => {
  const started: string[] = [];
  return withStarter(t.signal, { onStarted: (session) => started.push(session) }, async (container, services) => {
    services.places = [api];
    services.startError = new ConnectError("failed_precondition: dir is not under home", Code.FailedPrecondition);
    await ask(container, prompt);
    await untilTurn(t.signal, () => optionsOf(container).length === 1, "the proposals are listed");
    await click(startButton(container));
    await untilTurn(t.signal, () => alertTexts(container).length === 1, "the refusal is said");
    assert.deepEqual(alertTexts(container), [startFailure], "the refusal is the fixed line");
    assert.ok(!textOf(container).includes("dir is not under home"), "the server's own text is not shown");
    assert.equal(promptBox(container).value, prompt, "the prompt is kept");
    assert.equal(folderBox(container).value, "/work/api", "the folder is kept");
    assert.deepEqual(started, [], "no session is reported");
    services.startError = null;
    await click(startButton(container));
    await untilTurn(t.signal, () => started.length === 1, "the retried session is reported");
    assert.equal(services.startCalls.length, 2);
    assert.equal(services.startCalls[1].id, services.startCalls[0].id, "the retry names the same start id");
  });
});

test("HostsWebStarter: given a folder and a host, it opens with them typed, and Start is on once the prompt has words", (t) =>
  withStarter(t.signal, { folder: "/work/api", host: "dock" }, async (container) => {
    assertNone(container.querySelector('[role="listbox"]'), "no proposal is listed when the place is given");
    assert.equal(hostSelect(container).value, "dock", "the given host is chosen");
    assert.equal(folderBox(container).value, "/work/api", "the given folder is typed");
    assert.equal(startButton(container).disabled, true, "Start is off until the prompt has words");
    await typeInto(promptBox(container), prompt);
    assert.equal(startButton(container).disabled, false, "Start is on once the prompt has words");
  }));

test("HostsWebStarter: Escape closes it", (t) => {
  let closes = 0;
  return withStarter(t.signal, { onClose: () => (closes += 1) }, async (container) => {
    await pressKey(promptBox(container), "Escape");
    assert.equal(closes, 1, "Escape closes the Starter once");
  });
});

test("HostsWebStarter: ⌘⏎ and Ctrl+⏎ start it at the desk", (t) => {
  const started: string[] = [];
  return withStarter(t.signal, { folder: "/work/api", host: "halyard", onStarted: (session) => started.push(session) }, async (container, services) => {
    await typeInto(promptBox(container), prompt);
    await pressKey(promptBox(container), "Enter", { metaKey: true });
    await untilTurn(t.signal, () => started.length === 1, "⌘⏎ reports the session");
    await pressKey(promptBox(container), "Enter", { ctrlKey: true });
    await untilTurn(t.signal, () => started.length === 2, "Ctrl+⏎ reports the session");
    assert.deepEqual(services.proposeCalls, [], "⌘⏎ and Ctrl+⏎ start the session and do not propose a place");
    assert.deepEqual(
      services.startCalls.map((call) => [call.host, call.dir, call.prompt]),
      [
        ["halyard", "/work/api", prompt],
        ["halyard", "/work/api", prompt],
      ],
      "each start sends the place and the prompt",
    );
  });
});

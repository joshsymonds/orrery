// Starter: starting a session, a prompt first, then the host and folder it runs in. The prompt goes to Picker.Propose, whose
// proposals are a dropdown under it; choosing one sets the host and folder below, and typing a folder or choosing a host
// closes the dropdown. Only the reachable hosts are offered, so a proposal on any other host is dropped.

import type { JSX } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import * as actions from "./actions";
import type { Clients } from "./actions";
import type { StartProposal } from "./gen/orrery/v1/orrery_pb";
import { HostLabel } from "./hostlabel";
import { Icon } from "./glyphs";

// The one line a refused or failed Start leaves. It never shows the server's text.
const startFailedText = "The session did not start.";

// The one line a failed Propose leaves. It never shows the server's text.
const proposeFailedText = "The proposals did not come back. Your prompt is kept.";

// Phase is the Starter's state: the prompt before any Propose, the wait for one, the proposals it answered with, none
// that fit, a failed Propose, or a place typed by hand, which is also the state a Starter opens in when it is given one.
type Phase = "prompt" | "loading" | "several" | "none" | "failed" | "typed";

// Starter draws starting a session: a prompt field, then the place it runs, a host and a folder. ⏎ in the prompt sends
// it to Picker.Propose, and the answer's proposals on reachable hosts are a listbox under the prompt, the top one
// preselected and set as the host and folder below. Choosing another proposal sets them; typing a folder or choosing a
// host closes the listbox. Start is on once the prompt has words and there is a folder. It sends the host, the folder,
// the prompt and the start id made when the Starter opens to Control.Start, and onStarted receives the session it
// started. A refused or failed Start keeps everything and shows the fixed line. ⌘⏎ or Ctrl+⏎ starts it at the desk, and
// Escape closes it through onClose. A folder and a host given open the Starter with them typed.
export function Starter({
  clients,
  hosts,
  folder,
  host,
  sheet,
  onStarted,
  onClose,
}: {
  clients: Clients;
  hosts: string[];
  folder?: string;
  host?: string;
  sheet: boolean;
  onStarted: (session: string) => void;
  onClose: () => void;
}): JSX.Element {
  const [prompt, setPrompt] = useState("");
  const [phase, setPhase] = useState<Phase>(folder !== undefined || host !== undefined ? "typed" : "prompt");
  const [proposals, setProposals] = useState<StartProposal[]>([]);
  const [current, setCurrent] = useState(0);
  const [chosenHost, setChosenHost] = useState(host !== undefined && hosts.includes(host) ? host : (hosts[0] ?? ""));
  const [chosenFolder, setChosenFolder] = useState(folder ?? "");
  const [startId] = useState(() => actions.newStartId());
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  // propose sends the prompt to Picker.Propose and places the answer. A proposal on a host that is not reachable is
  // dropped; the top of those left is chosen, and none left says so with the folder cleared for typing.
  const propose = () => {
    if (phase === "loading" || prompt.trim() === "") {
      return;
    }
    setPhase("loading");
    void actions.propose(clients, prompt).then(
      (answer) => {
        const fitting = answer.filter((proposal) => hosts.includes(proposal.host));
        const top = fitting[0];
        setProposals(fitting);
        setCurrent(0);
        if (top === undefined) {
          setChosenFolder("");
          setPhase("none");
        } else {
          setChosenHost(top.host);
          setChosenFolder(top.dir);
          setPhase("several");
        }
      },
      () => setPhase("failed"),
    );
  };

  // choose sets the host and folder to the proposal at index, and keeps the listbox open.
  const choose = (index: number, proposal: StartProposal) => {
    setCurrent(index);
    setChosenHost(proposal.host);
    setChosenFolder(proposal.dir);
  };

  const canStart = phase !== "loading" && prompt.trim() !== "" && chosenFolder.trim() !== "";

  // start sends the chosen host, the folder, the prompt and the start id to Control.Start.
  const start = () => {
    if (starting || !canStart) {
      return;
    }
    setStarting(true);
    setStartError("");
    void actions.start(clients, chosenFolder.trim(), prompt, startId, chosenHost).then(
      (session) => {
        setStarting(false);
        onStarted(session);
      },
      () => {
        setStarting(false);
        setStartError(startFailedText);
      },
    );
  };

  // onKey closes the Starter on Escape, and starts it on ⌘⏎ or Ctrl+⏎ at the desk.
  const onKey = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      onClose();
    } else if (!sheet && event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      start();
    }
  };

  const dropdown =
    phase === "prompt" ? (
      <p class="inote">Press ⏎ and Orrery proposes where to run it.</p>
    ) : phase === "loading" ? (
      <p class="inote">Finding where to run it…</p>
    ) : phase === "several" ? (
      <div class="starter-list" role="listbox" aria-label="Proposals">
        {proposals.map((proposal, index) => {
          const isCurrent = index === current;
          return (
            <button
              key={index}
              type="button"
              role="option"
              aria-selected={isCurrent}
              class={isCurrent ? "pick-row cur" : "pick-row"}
              onClick={() => choose(index, proposal)}
            >
              <span class="k">start in</span>
              <span class="t">{proposal.dir}</span>
              <HostLabel name={proposal.host} />
            </button>
          );
        })}
      </div>
    ) : phase === "none" ? (
      <p class="inote">No reachable host has a folder that fits this. Choose a host and type a folder.</p>
    ) : phase === "failed" ? (
      <p class="inote error" role="alert">
        {proposeFailedText}
      </p>
    ) : null;

  const place =
    phase === "prompt" || phase === "loading" ? null : (
      <div class="starter-place">
        <div class="form-row">
          <span class="t-label">Host</span>
          <label class="field host-choice">
            <Icon name="host" />
            <select
              value={chosenHost}
              aria-label="Host"
              onChange={(event) => {
                setChosenHost(event.currentTarget.value);
                setPhase("typed");
              }}
            >
              {hosts.map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))}
            </select>
          </label>
        </div>
        <div class="form-row">
          <span class="t-label">Folder</span>
          <label class="field">
            <svg class="icon" viewBox="0 0 16 16" aria-hidden="true">
              <path d="M2 4h4l1.5 1.5H14v7H2z" />
            </svg>
            <input
              value={chosenFolder}
              placeholder="A folder on that host"
              aria-label="Folder"
              onInput={(event) => {
                setChosenFolder(event.currentTarget.value);
                setPhase("typed");
              }}
            />
          </label>
        </div>
        {phase === "typed" && chosenHost !== "" && (
          <p class="inote">The folder is checked on {chosenHost} when the session starts.</p>
        )}
      </div>
    );

  const body = (
    <>
      <label class="field">
        <svg class="icon" viewBox="0 0 16 16" aria-hidden="true">
          <path d="M7 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM10 10l3.5 3.5" />
        </svg>
        <input
          ref={inputRef}
          value={prompt}
          placeholder="What should it do? Say it in your words"
          aria-label="Prompt"
          onInput={(event) => setPrompt(event.currentTarget.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.metaKey && !event.ctrlKey) {
              event.preventDefault();
              propose();
            }
          }}
        />
        {phase === "loading" ? <span class="count">asking…</span> : <kbd>⏎</kbd>}
      </label>
      {dropdown}
      {place}
      <div class="foot">
        {phase === "failed" && (
          <button type="button" class="act" onClick={propose}>
            Try again
          </button>
        )}
        <span class="end">
          {starting && <span class="inote">starting…</span>}
          <button type="button" class="act primary" disabled={starting || !canStart} onClick={start}>
            Start the session
            {!sheet && <kbd>⌘⏎</kbd>}
          </button>
        </span>
      </div>
      {startError !== "" && (
        <p class="inote error" role="alert">
          {startError}
        </p>
      )}
    </>
  );

  return sheet ? (
    <div class="sheet-scrim">
      <section class="sheet" role="dialog" aria-label="Start a session" onKeyDown={onKey}>
        <div class="sheet-head">
          <b>Start a session</b>
        </div>
        <div class="sheet-body">{body}</div>
      </section>
    </div>
  ) : (
    <div class="scrim">
      <section class="panel" role="dialog" aria-label="Start a session" onKeyDown={onKey}>
        <div class="head">
          <h2 class="t-title">Start a session</h2>
          <button type="button" aria-label="Close" title="Close  esc" onClick={onClose}>
            <svg class="icon" viewBox="0 0 16 16" aria-hidden="true">
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          </button>
        </div>
        <div class="panel-body">{body}</div>
      </section>
    </div>
  );
}

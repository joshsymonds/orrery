import { ConnectError } from "@connectrpc/connect";
import type { JSX } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import type { createClients } from "./client";
import { ContractBody, type DocMarks } from "./contract";
import {
  acceptable,
  acceptStep,
  concernedRows,
  contractSlug,
  footText,
  leftOf,
  type Left,
  openDecisions,
  readKey,
  recordLine,
  type RowAbout,
} from "./contracts";
import { ContractWork } from "./contractwork";
import type { EpicContract } from "./gen/orrery/v1/epics_pb";
import type { Contract, ContractsGetResponse, ContractVersion } from "./gen/orrery/v1/contracts_pb";
import { clock } from "./reader";

// ContractRecordProps are what the contract panel gives the record of one contract: its id, the client that reads and
// settles it, the writer's name for a session, and what the page allows the record to do.
export interface ContractRecordProps {
  id: string;
  clients: Pick<ReturnType<typeof createClients>, "contracts">;
  writer: (session: string) => string;
  hasEpic: boolean;
  read: ReadonlySet<string>;
  onRead: (key: string) => void;
  live: boolean;
  onAbout?: (about: RowAbout) => void;
  // aboutId is the id of the row the dock's About names, whose control the record draws lit.
  aboutId?: string;
  onChanged?: () => void;
  onTalk?: () => void;
  // onOpenEpic opens the contract's epic, which the record links once the contract is accepted and has one.
  onOpenEpic?: (id: string) => void;
}

// Version is a version the record draws: one that holds its contract, which the record's body is drawn from.
type Version = ContractVersion & { contract: EpicContract };

// View is the contract as the record draws it: the Get it came from, its contract, its versions oldest first, the
// version drawn and the newest.
interface View {
  get: ContractsGetResponse;
  contract: Contract;
  versions: Version[];
  drawn: Version;
  newest: Version;
}

// newerNote is what the record says when Accept finds a newer version than the one drawn.
const newerNote = "A newer version came in. Read it before you accept.";

// versionsHead names the versions table's columns, round 6's: the version, when, by whom, and what changed.
const versionsHead = ["", "when", "by", "what changed"];

// noLive is the line the record draws when the conversation that wrote the contract has ended.
const noLive = "Its conversation has ended: you can accept it or set it aside, but not change it.";

// viewOf draws a Get's contract at its newest version. A Get with no contract, no version, or a version without its
// contract is unavailable.
function viewOf(response: ContractsGetResponse): View {
  const versions = response.versions.map((version) => {
    if (version.contract === undefined) {
      throw new Error("contract unavailable");
    }
    return { ...version, contract: version.contract };
  });
  const newest = versions.at(-1);
  if (response.contract === undefined || newest === undefined) {
    throw new Error("contract unavailable");
  }
  return { get: response, contract: response.contract, versions, drawn: newest, newest };
}

// leftOfView is what the drawn version still asks of its owner: its open decisions, and an unread irreversible section.
function leftOfView(view: View, read: ReadonlySet<string>): Left {
  const shown = read.has(readKey(view.contract.id, view.drawn.version));
  return leftOf(openDecisions(view.drawn.decisions), view.contract.care, shown);
}

// ContractRecord is a contract drawn as the contract document, read from Contracts.Get. Its newest version is drawn unless
// the viewer picks an older one, which is read-only. Accept, Set aside and Reopen each send one call, and a refusal's
// reason is shown in place of nothing looking done. The irreversible section is marked read once it is on screen.
//
// An accepted contract whose Get carries work opens on the work by requirement, and the document follows it on the same
// page.
export function ContractRecord(props: ContractRecordProps): JSX.Element {
  const { id, clients, writer, hasEpic, read, onRead, live, onAbout, aboutId, onChanged, onTalk, onOpenEpic } = props;
  const [loaded, setLoaded] = useState<View | undefined>(undefined);
  const [loadError, setLoadError] = useState("");
  const [sending, setSending] = useState(false);
  const [refusal, setRefusal] = useState("");
  const [note, setNote] = useState("");
  const [versionsOpen, setVersionsOpen] = useState(false);
  const [setAsideOpen, setSetAsideOpen] = useState(false);
  const [words, setWords] = useState("");
  const irreversibleRef = useRef<HTMLElement>(null);
  // A view is drawn only for the contract the record is given, never one loaded for another id.
  const view = loaded !== undefined && loaded.contract.id === id ? loaded : undefined;

  useEffect(() => {
    let current = true;
    setLoaded(undefined);
    setLoadError("");
    setRefusal("");
    setNote("");
    setVersionsOpen(false);
    setSetAsideOpen(false);
    setWords("");
    const load = async (): Promise<void> => {
      try {
        const next = viewOf(await clients.contracts.get({ id }));
        if (current) {
          setLoaded(next);
        }
      } catch (error) {
        if (current) {
          setLoadError(ConnectError.from(error).rawMessage);
        }
      }
    };
    void load();
    return () => {
      current = false;
    };
  }, [id, clients]);

  const watching = view !== undefined && view.contract.state === "proposed" && leftOfView(view, read).unread;
  const key = view === undefined ? "" : readKey(view.contract.id, view.drawn.version);
  useEffect(() => {
    const Observer = globalThis.IntersectionObserver as typeof IntersectionObserver | undefined;
    const section = irreversibleRef.current;
    if (!watching || Observer === undefined || section === null) {
      return undefined;
    }
    const observer = new Observer((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) {
        observer.disconnect();
        onRead(key);
      }
    });
    observer.observe(section);
    return () => observer.disconnect();
  }, [watching, key, onRead]);

  if (view === undefined) {
    return (
      <div class="contract-record">
        {loadError === "" ? (
          <p class="inote">
            <span>Reading the contract…</span>
          </p>
        ) : (
          <p class="inote error" role="alert">
            <span>contract unavailable</span> <span>{loadError}</span>
          </p>
        )}
      </div>
    );
  }

  // send runs one call the viewer made: nothing shows done until it answers, and a refusal's reason shows instead.
  const send = async (call: () => Promise<void>): Promise<void> => {
    setSending(true);
    setRefusal("");
    setNote("");
    try {
      await call();
    } catch (error) {
      setRefusal(ConnectError.from(error).rawMessage);
    } finally {
      setSending(false);
    }
  };

  // reload draws the contract again, at its newest version, once a write has changed it.
  const reload = async (): Promise<void> => {
    setLoaded(viewOf(await clients.contracts.get({ id })));
  };

  const { get, contract, versions, drawn, newest } = view;
  const isNewest = drawn.version === newest.version;
  const proposed = contract.state === "proposed";
  const left = leftOfView(view, read);
  const acceptOk = acceptable(get, drawn.version, read, sending);
  const canSetAside = isNewest && (proposed || (contract.state === "accepted" && !hasEpic));
  const canReopen = isNewest && contract.state === "set_aside";
  const talking = live && proposed && isNewest ? onAbout : undefined;
  // An accepted contract with work opens on the work, whose door is the epic the page can open.
  const showWork = contract.state === "accepted" && get.work.length > 0;
  const workEpic = hasEpic && onOpenEpic !== undefined ? { id, onOpen: () => onOpenEpic(id) } : null;

  // accept re-reads the contract at the press. While it is still proposed at the version drawn, it sends one Accept
  // with no words. Otherwise it sends nothing and draws the newer answer with its note.
  const accept = (): Promise<void> =>
    send(async () => {
      const reread = await clients.contracts.get({ id });
      const step = acceptStep(drawn.version, reread);
      if (step.kind === "redraw") {
        setLoaded(viewOf(step.get));
        setNote(newerNote);
        return;
      }
      await clients.contracts.accept({ id, words: "" });
      onChanged?.();
      await reload();
    });

  const setAside = (): Promise<void> =>
    send(async () => {
      await clients.contracts.setAside({ id, words });
      setSetAsideOpen(false);
      setWords("");
      onChanged?.();
      await reload();
    });

  const reopen = (): Promise<void> =>
    send(async () => {
      await clients.contracts.reopen({ id, words: "" });
      onChanged?.();
      await reload();
    });

  const versionsLabel = versions.length === 1 ? "1 version" : `${versions.length} versions`;
  const facts = (
    <>
      <div class="facts rec">
        <span class="by">
          <span>{`written by ${writer(versions[0].writer)}`}</span>
        </span>
        <button type="button" class="link" aria-expanded={versionsOpen} onClick={() => setVersionsOpen(!versionsOpen)}>
          {versionsLabel}
        </button>
        {contract.state === "accepted" && hasEpic && onOpenEpic !== undefined && (
          <button type="button" class="link" onClick={() => onOpenEpic(id)}>
            {`its epic, ${contractSlug(id)}`}
          </button>
        )}
      </div>
      {versionsOpen && (
        <div class="tbl versions">
          <div class="tr head">
            {versionsHead.map((name) => (
              <span>{name}</span>
            ))}
          </div>
          {versions
            .slice()
            .reverse()
            .map((version) => (
              <button type="button" class="tr" onClick={() => setLoaded({ ...view, drawn: version })}>
                <span class={version.version === newest.version ? "chip new" : "chip"}>{`v${version.version}`}</span>
                <span class="id">{clock(version.time)}</span>
                <span class="wide">{writer(version.writer)}</span>
                <span class="wide">
                  {version.changedRows.map((row) => (
                    <span class="idc" data-k={row.charAt(0)}>
                      {row}
                    </span>
                  ))}
                  {` ${version.words}`}
                </span>
              </button>
            ))}
        </div>
      )}
    </>
  );

  const marks: DocMarks = {
    concerned: concernedRows(drawn.decisions),
    unread: left.unread,
    changedRows: new Set(drawn.changedRows),
    changedSections: new Set(drawn.changedSections),
    version: Number(drawn.version),
  };

  return (
    <div class="contract-record">
      {showWork && <ContractWork work={get.work} epic={workEpic} />}
      {!isNewest && (
        <p class="inote banner">
          <span>{`v${drawn.version} of ${newest.version} · not the newest`}</span>{" "}
          <button type="button" class="link" onClick={() => setLoaded({ ...view, drawn: newest })}>
            Read the newest
          </button>
        </p>
      )}
      <ContractBody
        contract={drawn.contract}
        requirements={drawn.requirements}
        decisions={drawn.decisions}
        record={recordLine(get)}
        facts={facts}
        marks={marks}
        attending={proposed && isNewest}
        aboutId={aboutId}
        onAbout={talking}
        irreversibleRef={irreversibleRef}
      />
      <div class="cmdbar">
        <p class="inote">
          <span>{proposed ? footText(left) : recordLine(get)}</span>
        </p>
        <span class="end">
          {onTalk !== undefined && (
            <button type="button" class="btn" onClick={onTalk}>
              Talk about it
            </button>
          )}
          {proposed && (
            <button type="button" class="btn primary" disabled={!acceptOk} onClick={() => void accept()}>
              Accept
            </button>
          )}
          {canSetAside && !setAsideOpen && (
            <button type="button" class="btn" disabled={sending} onClick={() => setSetAsideOpen(true)}>
              Set aside…
            </button>
          )}
          {setAsideOpen && (
            <>
              <input
                type="text"
                class="field"
                aria-label="Why you set it aside"
                value={words}
                disabled={sending}
                onInput={(event) => setWords(event.currentTarget.value)}
              />
              <button
                type="button"
                class="btn"
                disabled={sending || words.trim() === ""}
                onClick={() => void setAside()}
              >
                Set aside
              </button>
              <button
                type="button"
                class="btn quiet"
                disabled={sending}
                onClick={() => {
                  setSetAsideOpen(false);
                  setWords("");
                }}
              >
                Cancel
              </button>
            </>
          )}
          {canReopen && (
            <button type="button" class="btn" disabled={sending} onClick={() => void reopen()}>
              Reopen
            </button>
          )}
        </span>
      </div>
      {refusal !== "" && (
        <p class="inote error" role="alert">
          <span>{refusal}</span>
        </p>
      )}
      {note !== "" && (
        <p class="inote">
          <span>{note}</span>
        </p>
      )}
      {!live && proposed && (
        <p class="inote">
          <span>{noLive}</span>
        </p>
      )}
    </div>
  );
}

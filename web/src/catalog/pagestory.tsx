// PageStory draws App as one page of a catalog. It draws App from the start, as main.tsx does before its first load has
// answered, so the inbox says loading until then. It reads the rows, the epics and the host over a fixture transport as
// main.tsx's start does, watches the notices showing over the same transport as main.tsx does, then gives App them at the
// address it is given, which stays that address for the story's whole life. A list that failed is drawn as main.tsx
// draws it. A call the fixture refuses draws a failure element that names it.

import type { VNode } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import { App } from "../app";
import type { createClients } from "../client";
import { type EpicsView, epicsView } from "../epics";
import { liveEpoch } from "../fixtures/clock";
import { createFixtureTransport, type FixtureTransport } from "../fixtures/transport";
import type { Contract } from "../gen/orrery/v1/contracts_pb";
import type { InAppNotice } from "../gen/orrery/v1/notice_pb";
import type { HostStatusResponse } from "../gen/orrery/v1/orrery_pb";
import { loadRows } from "../load";
import { changedOnly, loadEpics } from "../loadepics";
import { watchNotices } from "../notices";
import type { Row } from "../rows";
import { type Address, formatAddress } from "../router";

interface PageStoryProps {
  // address is the address App is given. App never reports a change back into it.
  address: Address;
  // after runs once the first load has drawn, with the element App is drawn in, to reach a state no address names. A
  // rejection is reported with console.error.
  after?: (root: HTMLElement) => Promise<void>;
  // fixture is the fixture transport the story reads from. A fresh one is made when none is given.
  fixture?: FixtureTransport;
}

// Loaded is what the first load answers with, held as main.tsx's start holds it. listFailed is whether the rows' load or
// the epics' load failed.
interface Loaded {
  rows: Row[];
  listFailed: boolean;
  epics: EpicsView;
  contracts: Contract[];
  host: HostStatusResponse | null;
}

// now is the instant the fixtures are set at, in unix seconds, as App takes it.
const now = Date.parse(liveEpoch) / 1000;

// retryMs is how long a notices stream that fails waits before it reopens, as main.tsx waits. The fixture's streams never
// fail, so the wait never runs.
const retryMs = 1000;

const noop = () => {};

// load reads the rows, the epics and the host over clients, as main.tsx's start reads them.
async function load(clients: ReturnType<typeof createClients>): Promise<Loaded> {
  const [rows, epics, host] = await Promise.all([
    loadRows(clients),
    loadEpics(clients, new Map(), changedOnly),
    clients.host.status({}).catch(() => null),
  ]);
  return {
    rows: rows.rows,
    listFailed: rows.error !== "" || epics.error !== "",
    epics: epicsView(epics.epics, epics.details),
    contracts: epics.contracts,
    host,
  };
}

// messageOf is the message of an error, or the error's text when it is not an Error.
function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function PageStory({ address, after, fixture }: PageStoryProps): VNode {
  const [transport] = useState(() => fixture ?? createFixtureTransport());
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  // The notices showing, as Notices.Watch sends them over the fixture.
  const [notices, setNotices] = useState<InAppNotice[]>([]);
  // The count of refused calls heard. A change of it draws the page again, which reads the unserved list.
  const [, setHeard] = useState(0);
  const root = useRef<HTMLDivElement>(null);

  // The subscription is made before the load, so a call the load makes is heard as it is made.
  useEffect(() => transport.onUnserved(() => setHeard((count) => count + 1)), [transport]);

  useEffect(() => {
    let live = true;
    load(transport.clients).then(
      (next) => {
        if (live) {
          setLoaded(next);
        }
      },
      (error: unknown) => {
        console.error(`story ${formatAddress(address)}: the first load failed: ${messageOf(error)}`);
      },
    );
    return () => {
      live = false;
    };
  }, [transport]);

  // The notices watch is opened once, and aborted on unmount, so nothing reaches the page after it.
  useEffect(() => {
    const controller = new AbortController();
    void watchNotices(transport.clients, setNotices, retryMs, controller.signal);
    return () => controller.abort();
  }, [transport]);

  // after runs once the first load has drawn. A synchronous throw from it is a rejection too, so both are reported.
  useEffect(() => {
    const element = root.current;
    if (loaded === null || after === undefined || element === null) {
      return;
    }
    new Promise<void>((resolve) => resolve(after(element))).catch((error: unknown) => {
      console.error(`story ${formatAddress(address)}: ${messageOf(error)}`);
    });
  }, [loaded]);

  const refused = transport.unserved;
  // data-loaded is set once the first load has answered, so a test can wait for the page's data. A story reads once, so
  // its Try now is drawn as round 6 draws it and reads nothing again.
  return (
    <div ref={root} data-page-story="" data-loaded={loaded !== null ? "" : undefined}>
      <App
        rows={loaded?.rows ?? []}
        answered={loaded !== null}
        listFailed={loaded?.listFailed ?? false}
        epics={loaded?.epics}
        contracts={loaded?.contracts}
        now={now}
        clients={transport.clients}
        notices={notices}
        host={loaded?.host}
        onViewing={noop}
        onRetry={noop}
        address={address}
        onAddress={noop}
      />
      {refused.length > 0 && <p data-story-failure="">{`not served by the fixture: ${refused.join(", ")}`}</p>}
    </div>
  );
}

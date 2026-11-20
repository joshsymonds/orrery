// PageStory draws App as one page of a catalog. It reads the rows, the epics and the host over a fixture transport as
// main.tsx's start does, then draws App with them at the address it is given, which stays that address for the story's
// whole life. A call the fixture refuses draws a failure element that names it.

import type { VNode } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import { App } from "../app";
import type { createClients } from "../client";
import { type EpicsView, epicsView } from "../epics";
import { liveEpoch } from "../fixtures/clock";
import { createFixtureTransport, type FixtureTransport } from "../fixtures/transport";
import type { Contract } from "../gen/orrery/v1/contracts_pb";
import type { HostStatusResponse } from "../gen/orrery/v1/orrery_pb";
import { loadRows } from "../load";
import { changedOnly, loadEpics } from "../loadepics";
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

// Loaded is what the first load answers with, held as main.tsx's start holds it.
interface Loaded {
  rows: Row[];
  error: string;
  epics: EpicsView;
  contracts: Contract[];
  host: HostStatusResponse | null;
}

// now is the instant the fixtures are set at, in unix seconds, as App takes it.
const now = Date.parse(liveEpoch) / 1000;

const noop = () => {};

// load reads the rows, the epics and the host over clients, as main.tsx's start reads them. The error is the rows' error
// and the epics' error, the non-empty ones joined by "; ".
async function load(clients: ReturnType<typeof createClients>): Promise<Loaded> {
  const [rows, epics, host] = await Promise.all([
    loadRows(clients),
    loadEpics(clients, new Map(), changedOnly),
    clients.host.status({}).catch(() => null),
  ]);
  return {
    rows: rows.rows,
    error: [rows.error, epics.error].filter((text) => text !== "").join("; "),
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
  return (
    <div ref={root} data-page-story="">
      {loaded !== null && (
        <App
          rows={loaded.rows}
          error={loaded.error}
          epics={loaded.epics}
          contracts={loaded.contracts}
          now={now}
          clients={transport.clients}
          notices={[]}
          host={loaded.host}
          onViewing={noop}
          address={address}
          onAddress={noop}
        />
      )}
      {refused.length > 0 && <p data-story-failure="">{`not served by the fixture: ${refused.join(", ")}`}</p>}
    </div>
  );
}

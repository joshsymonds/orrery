// The stories of the proof sections of an epic page: the red and green cards, reached or dim; the release steps,
// released, in progress, waiting, or with no contract; and the report, written, not shipped, or unwritten. The cards
// take the requirements and the wedges the page derives from the fixture epic's detail.

import type { Stories } from "./catalog/story";
import type { LoadArtifact } from "./evidence";
import { gets } from "./fixtures/epics";
import { createFixtureTransport } from "./fixtures/transport";
import type { EpicsGetResponse } from "./gen/orrery/v1/epics_pb";
import { wedgesOf } from "./phases";
import {
  GreenProof as GreenCards,
  RedProof as RedCards,
  ReleaseProof as ReleaseSteps,
  Report as ReportSection,
} from "./proof";

const rota = "lighthouse/rota-export";
const holder = "orrery/holder-scopes";
const tide = "tidepool/tide-alerts";
const offline = "tidepool/offline-tables";
const orbit = "orrery/orbit-labels";
const stopped = "tidepool/tide-cache";

const clients = createFixtureTransport().clients;

const noop = () => {};

const loadArtifact: LoadArtifact = (run, name, stderr) => clients.evidence.artifact({ run, name, stderr });

// detailOf is the Epics.Get answer of an epic the fixtures hold.
function detailOf(id: string): EpicsGetResponse {
  const detail = gets.get(id);
  if (detail === undefined) {
    throw new Error(`the fixtures hold no detail for ${id}`);
  }
  return detail;
}

// shapeOf are an epic's requirements and the wedges its page derives from its detail, as the card sections take them.
function shapeOf(id: string) {
  const detail = detailOf(id);
  return { requirements: detail.requirements, wedges: wedgesOf(detail) };
}

// releaseOf are an epic's contract, release steps and whether it is released, as the release section takes them.
function releaseOf(detail: EpicsGetResponse) {
  return {
    contract: detail.contract,
    releaseSteps: detail.releaseSteps,
    released: detail.epic?.outcome.startsWith("released") === true,
  };
}

export const RedProof: Stories = {
  // reached is an epic whose requirements have red runs, so its section is drawn in full, and each card names its
  // wedge.
  reached: () => <RedCards {...shapeOf(rota)} loadArtifact={loadArtifact} onOpenPhase={noop} />,
  // dim is an epic whose requirements have no red run, so its section is drawn dim.
  dim: () => <RedCards {...shapeOf(offline)} loadArtifact={loadArtifact} onOpenPhase={noop} />,
};

export const GreenProof: Stories = {
  // reached is an epic whose green run passed, so its section is drawn in full.
  reached: () => <GreenCards {...shapeOf(holder)} loadArtifact={loadArtifact} onOpenPhase={noop} />,
  // dim is an epic with no run yet, so its section is drawn dim.
  dim: () => <GreenCards {...shapeOf(tide)} loadArtifact={loadArtifact} onOpenPhase={noop} />,
};

export const ReleaseProof: Stories = {
  // released is an epic whose outcome is released, with each step passed and its evidence and reason drawn.
  released: () => <ReleaseSteps {...releaseOf(detailOf(holder))} />,
  // in-progress is an epic not yet released whose first step passed, so its section is drawn in full.
  "in-progress": () => {
    const detail = detailOf(rota);
    const releaseSteps = detail.releaseSteps.map((proof, index) =>
      index === 0 ? { ...proof, state: "passed", evidence: "github · 14:05 · signature good" } : proof,
    );
    return <ReleaseSteps {...releaseOf({ ...detail, releaseSteps })} />;
  },
  // waiting is an epic whose release steps have not begun, so its section is drawn dim.
  waiting: () => <ReleaseSteps {...releaseOf(detailOf(tide))} />,
  // no-contract is an epic with release steps and no contract, so its section is its heading alone.
  "no-contract": () => <ReleaseSteps {...releaseOf(detailOf(orbit))} />,
};

export const Report: Stories = {
  // written is a released epic's report: its outcome, the requirements it shipped, and why.
  written: () => <ReportSection report={detailOf(holder).report} />,
  // not-shipped is an ended epic's report, which names requirements not shipped and no shipped ones.
  "not-shipped": () => <ReportSection report={detailOf(stopped).report} />,
  // unwritten is an epic with no report yet, so its section is its heading alone, dim.
  unwritten: () => <ReportSection report={detailOf(rota).report} />,
};

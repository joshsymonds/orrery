import type { JSX } from "preact";
import { useState } from "preact/hooks";

import { EvidenceRuns, type LoadArtifact } from "./evidence";
import type { EpicRequirement, EvidenceRun } from "./gen/orrery/v1/epics_pb";

// Proof is one proof cell: its tone, its glyph (empty when the tone has none) and its words.
type Proof = { tone: "none" | "live" | "ok" | "bad"; glyph: string; word: string };

function none(word: string): Proof {
  return { tone: "none", glyph: "", word };
}

function live(word: string): Proof {
  return { tone: "live", glyph: "", word };
}

function pass(word: string): Proof {
  return { tone: "ok", glyph: "✓", word };
}

function fail(word: string): Proof {
  return { tone: "bad", glyph: "✗", word };
}

// RequirementTable draws an epic's requirements as one table: each row its id, what will be true when done, its red and
// green proof, and proven or open. A row is a button: pressed, it opens to its evidence runs, and pressed again it
// closes. initiallyOpen names the row drawn open.
export function RequirementTable(props: {
  requirements: EpicRequirement[];
  loadArtifact: LoadArtifact;
  initiallyOpen?: string;
}): JSX.Element {
  const { requirements, loadArtifact, initiallyOpen } = props;
  return (
    <div class="tbl reqs">
      <div class="th">
        <span />
        <span>what will be true when done</span>
        <span class="th-red">red</span>
        <span class="th-green">green</span>
        <span />
      </div>
      {requirements.map((requirement) => (
        <RequirementRow
          key={requirement.id}
          requirement={requirement}
          loadArtifact={loadArtifact}
          initiallyOpen={requirement.id === initiallyOpen}
        />
      ))}
    </div>
  );
}

// RequirementRow is one requirement: its button row, and under it its runs while the row is open.
function RequirementRow(props: {
  requirement: EpicRequirement;
  loadArtifact: LoadArtifact;
  initiallyOpen: boolean;
}): JSX.Element {
  const { requirement, loadArtifact, initiallyOpen } = props;
  const [open, setOpen] = useState(initiallyOpen);
  const proven = requirement.runs.some((run) => run.phase === "green" && run.passed);
  return (
    <>
      <button type="button" class="tr" aria-expanded={open} onClick={() => setOpen((isOpen) => !isOpen)}>
        <span class="id">{requirement.id}</span>
        <span class="strong wide">{requirement.text}</span>
        <ProofCell proof={redProofOf(requirement)} />
        <ProofCell proof={greenProofOf(requirement)} />
        <span class="end">{proven ? "proven" : "open"}</span>
      </button>
      {open && (
        <div class="tr-detail">
          <EvidenceRuns runs={runsInOrder(requirement.runs)} loadArtifact={loadArtifact} />
        </div>
      )}
    </>
  );
}

// ProofCell is one proof cell: its glyph in an aria-hidden span before its words.
function ProofCell(props: { proof: Proof }): JSX.Element {
  const { tone, glyph, word } = props.proof;
  return (
    <span class={`cell ${tone}`}>
      {glyph !== "" && <span aria-hidden="true">{glyph}</span>}
      {word}
    </span>
  );
}

// redProofOf is a requirement's red cell, from its newest red run.
function redProofOf(requirement: EpicRequirement): Proof {
  const red = newestRun(requirement.runs, "red");
  if (red === undefined) {
    return none(requirement.kind === "review" ? "by review" : "no test yet");
  }
  if (red.state !== "ended") {
    return live("running");
  }
  return red.passed ? pass("fails at base") : fail("passes at base");
}

// greenProofOf is a requirement's green cell, from its newest green run.
function greenProofOf(requirement: EpicRequirement): Proof {
  const green = newestRun(requirement.runs, "green");
  if (green === undefined) {
    return none(requirement.kind === "review" ? "by review" : "not yet");
  }
  if (green.state !== "ended") {
    return live("running");
  }
  return green.passed ? pass("passes") : fail("fails");
}

// newestRun is the run of a phase created last, whatever order the runs are in, or undefined when there is none.
function newestRun(runs: EvidenceRun[], phase: string): EvidenceRun | undefined {
  let newest: EvidenceRun | undefined;
  for (const run of runs) {
    if (run.phase === phase && (newest === undefined || run.created > newest.created)) {
      newest = run;
    }
  }
  return newest;
}

// runsInOrder is a requirement's runs red first then green, each phase in created order.
function runsInOrder(runs: EvidenceRun[]): EvidenceRun[] {
  const rank = (run: EvidenceRun) => (run.phase === "red" ? 0 : 1);
  return [...runs].sort((a, b) => rank(a) - rank(b) || (a.created < b.created ? -1 : a.created > b.created ? 1 : 0));
}

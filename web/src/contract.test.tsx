import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { render } from "preact";

import { type DocMarks, ContractBody } from "./contract";
import type { RowAbout } from "./contracts";
import type { ContractVersion, ContractsGetResponse } from "./gen/orrery/v1/contracts_pb";
import { type EpicContract, EpicContractSchema, type EpicRequirement, EpicRequirementSchema } from "./gen/orrery/v1/epics_pb";
import { tideAccepted, tideProposed, tideReady, tideSetAside } from "./fixtures/contractrecords";
import { clock } from "./reader";
import { installTestDom, removeTestDom } from "./testdom";

type Props = Parameters<typeof ContractBody>[0];

// acceptedAt is the unix second the synthetic contract was accepted at.
const acceptedAt = 1_783_000_000n;

// sectionNames are the ten sections of a contract's document, in the order the document draws them.
const sectionNames = [
  "What you asked for",
  "What could go wrong, and how much we care",
  "Things you did not ask for",
  "What will be true when done",
  "What I’m assuming",
  "What we won’t do",
  "How, and why not the other ways",
  "What leaves this machine or can’t be undone",
  "Decisions I need from you",
  "Checks the machines run",
];

// fields is a synthetic accepted contract, with a value in every field the document shows.
const fields: MessageInitShape<typeof EpicContractSchema> = {
  title: "Show the accepted contract",
  approved: "approved by you",
  intent: "Show the accepted contract read-only.",
  failures: [
    { id: "F1", text: "The page drifts from the contract.", rating: "limited", response: "prevent: a test" },
    { id: "F2", text: "A field goes missing.", rating: "serious", response: "detect: the section test" },
  ],
  care: "serious",
  additions: [{ location: "web/src/contract.tsx", what: "a read-only body", why: "the summary opens it", cost: "one component" }],
  premises: [{ id: "P1", text: "The contract is final once accepted.", ifWrong: "the page shows a stale copy." }],
  exclusions: ["no Accept button", "no editing"],
  approach: "Render the stored contract as text.",
  alternatives: [{ alternative: "Render markdown", whyNot: "It would parse markup.", reconsider: "If the contract gains formatting." }],
  releaseSteps: [{ step: "Deploy the web bundle", target: "the web host", undo: "redeploy the last bundle" }],
  releaseThen: "Then the page is live.",
  checks: "go test ./...\njust check",
  base: "0123456789abcdef0123456789abcdef01234567",
  accepted: acceptedAt,
};

const requirements: EpicRequirement[] = [
  create(EpicRequirementSchema, { id: "R1", text: "The page opens with the title.", evidence: "the title test" }),
  create(EpicRequirementSchema, { id: "R2", text: "No field is editable.", evidence: "the read-only test" }),
];

// withDoc draws ContractBody with props on a fresh DOM, runs check on it, and removes the DOM however the check ends.
async function withDoc(props: Props, check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  try {
    render(<ContractBody {...props} />, container);
    check(container);
  } finally {
    render(null, container);
    await removeTestDom(window);
  }
}

// newestOf is the newest version of a contract record, which the document draws.
function newestOf(get: ContractsGetResponse): ContractVersion {
  const newest = get.versions[get.versions.length - 1];
  assert.ok(newest !== undefined, "the record holds a version");
  return newest;
}

// contractOf is the contract a version holds, which the record always fills.
function contractOf(version: ContractVersion): EpicContract {
  assert.ok(version.contract !== undefined, "the version holds a contract");
  return version.contract;
}

// propsOf is the props that draw a record's newest version, with extra props on top.
function propsOf(get: ContractsGetResponse, extra: Partial<Props> = {}): Props {
  const version = newestOf(get);
  return { contract: contractOf(version), requirements: version.requirements, decisions: version.decisions, ...extra };
}

// marksOf are the marks a record's newest version gives the document: its changed rows and sections, its open
// decisions' rows as concerned, and whether its irreversible section is unread.
function marksOf(get: ContractsGetResponse, unread: boolean): DocMarks {
  const version = newestOf(get);
  return {
    concerned: new Set(version.decisions.filter((decision) => decision.answer === "").map((decision) => decision.row)),
    unread,
    changedRows: new Set(version.changedRows),
    changedSections: new Set(version.changedSections),
    version: Number(version.version),
  };
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// all returns the elements under root that match selector, in document order.
function all(root: ParentNode, selector: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(selector));
}

// textsOf returns the text of each element under root that matches selector.
function textsOf(root: ParentNode, selector: string): (string | null)[] {
  return all(root, selector).map((element) => element.textContent);
}

// sectionOf is the section of the document with the given name.
function sectionOf(container: HTMLElement, name: string): HTMLElement {
  return one(container, `section[aria-label="${name}"]`);
}

// columnsOf are the column names of a section's table, the empty id column left out.
function columnsOf(section: HTMLElement): (string | null)[] {
  return textsOf(section, ".tr.head > span").filter((text) => text !== "");
}

// toneOf is the tone a chip carries: bad, live, or none for a quiet chip.
function toneOf(chip: HTMLElement): string {
  if (chip.classList.contains("bad")) {
    return "bad";
  }
  return chip.classList.contains("live") ? "live" : "";
}

// talkOf is the row control of the row with the given id, or of the decision or requirement that has it.
function talkOf(container: HTMLElement, id: string): HTMLElement {
  return one(container, `.tr[data-row="${id}"] button[aria-label="Talk about this row"]`);
}

test("ContractDoc: the document draws its decision line, then the ten sections in order under their names", () =>
  withDoc(propsOf(tideProposed), (container) => {
    assert.equal(textsOf(container, "article.contract > section.dline h2")[0], "Alert the harbour office within a minute when a published tide table changes.");
    assert.deepEqual(
      all(container, "article.contract > section[aria-label]").map((section) => section.getAttribute("aria-label")),
      sectionNames,
    );
    assert.deepEqual(textsOf(container, "article.contract > section[aria-label] > .sec > .name"), sectionNames);
  }));

test("ContractDoc: each section's table draws its column names in order", () =>
  withDoc(propsOf(tideProposed), (container) => {
    assert.deepEqual(columnsOf(sectionOf(container, sectionNames[1])), ["if this happened", "how bad", "what we do"]);
    assert.deepEqual(columnsOf(sectionOf(container, sectionNames[2])), ["where", "what", "why", "cost"]);
    assert.deepEqual(columnsOf(sectionOf(container, sectionNames[3])), ["must be true", "how we’ll know"]);
    assert.deepEqual(columnsOf(sectionOf(container, sectionNames[4])), ["assumption", "if wrong"]);
    assert.deepEqual(columnsOf(sectionOf(container, sectionNames[6])), ["alternative", "why not", "reconsider when"]);
    assert.deepEqual(columnsOf(sectionOf(container, sectionNames[7])), ["step", "target", "undo"]);
    assert.deepEqual(columnsOf(sectionOf(container, sectionNames[8])), ["question", "recommended", "why"]);
  }));

test("ContractDoc: each failure's how-bad chip carries the tone of its rating", () =>
  withDoc(propsOf(tideProposed), (container) => {
    const rows = all(sectionOf(container, sectionNames[1]), ".tr[data-row]");
    assert.deepEqual(
      rows.map((row) => [row.getAttribute("data-row"), one(row, ".chip").textContent, toneOf(one(row, ".chip"))]),
      [
        ["F1", "limited", ""],
        ["F2", "serious", "live"],
        ["F3", "severe", "bad"],
      ],
    );
  }));

test("ContractDoc: each id chip carries its kind in data-k", () =>
  withDoc(propsOf(tideProposed), (container) => {
    assert.deepEqual(
      all(container, ".idc").map((chip) => [chip.textContent, chip.getAttribute("data-k")]),
      [
        ["F1", "F"],
        ["F2", "F"],
        ["F3", "F"],
        ["R1", "R"],
        ["R2", "R"],
        ["R3", "R"],
        ["R4", "R"],
        ["R5", "R"],
        ["P1", "P"],
        ["P2", "P"],
        ["D1", "D"],
      ],
    );
  }));

test("ContractDoc: the decision line draws the care chip, decisions needed and the record of an open contract", () =>
  withDoc(propsOf(tideProposed, { record: "v2 proposed 14:39 · first written 14:15" }), (container) => {
    assert.deepEqual(textsOf(container, ".dline .facts > span"), [
      "Level of care severe",
      "Decisions needed: 1",
      "v2 proposed 14:39 · first written 14:15",
    ]);
    assert.equal(toneOf(one(container, ".dline .facts .chip")), "bad");
  }));

test("ContractDoc: an accepted contract draws its approved line, Decisions needed none, and its base as a commit mark", () => {
  const version = newestOf(tideAccepted);
  const base = contractOf(version).base;
  return withDoc(propsOf(tideAccepted, { record: "v2 accepted 14:40 with “Accept, with the next two days only.”" }), (container) => {
    assert.equal(textsOf(container, ".dline > p.approved")[0], contractOf(version).approved);
    assert.deepEqual(textsOf(container, ".dline .facts > span"), [
      "Level of care severe",
      "Decisions needed: none",
      "v2 accepted 14:40 with “Accept, with the next two days only.”",
      "⎇",
    ]);
    const mark = one(container, ".dline .facts .commit-mark");
    assert.equal(mark.getAttribute("aria-label"), base);
    assert.equal(textsOf(container, ".dline .facts")[0]?.includes(base), false, "the base is not visible text");
  });
});

test("ContractDoc: the facts a caller gives draw last in the decision line", () =>
  withDoc(propsOf(tideProposed, { facts: <span class="extra">written by the session</span> }), (container) => {
    const facts = all(container, ".dline .facts > span");
    assert.equal(facts[facts.length - 1]?.textContent, "written by the session");
  }));

test("ContractDoc: with no open decision the section says None and each settled decision's answer", () =>
  withDoc(propsOf(tideReady), (container) => {
    const section = sectionOf(container, sectionNames[8]);
    assert.deepEqual(textsOf(section, ".inote"), ["None. Settled: D1 The next two days only"]);
    assert.equal(section.querySelectorAll(".tr[data-row]").length, 0);
  }));

test("ContractDoc: an Epics.Get contract draws the same document, its Decisions section reads None, and it has no row controls", () =>
  withDoc({ contract: create(EpicContractSchema, fields), requirements }, (container) => {
    assert.deepEqual(
      all(container, "article.contract > section[aria-label]").map((section) => section.getAttribute("aria-label")),
      sectionNames,
    );
    assert.deepEqual(textsOf(sectionOf(container, sectionNames[8]), ".inote"), ["None."]);
    assert.deepEqual(textsOf(container, ".dline .facts > span"), [
      "Level of care serious",
      "Decisions needed: none",
      `accepted ${clock(acceptedAt)}`,
      "⎇",
    ]);
    assert.equal(container.querySelectorAll("button").length, 0);
  }));

test("ContractDoc: each field's text lands in its section", () =>
  withDoc(propsOf(tideProposed), (container) => {
    assert.equal(textsOf(container, ".intent")[0], contractOf(newestOf(tideProposed)).intent);
    assert.deepEqual(textsOf(sectionOf(container, sectionNames[2]), ".tr .wide"), [
      "alerts/send.go",
      "Retry a failed send three times.",
      "The office’s endpoint dropped 2 of the last 100 test sends.",
      "One file. No new service.",
    ]);
    assert.deepEqual(textsOf(sectionOf(container, sectionNames[3]), ".tr[data-row] .strong"), [
      "A changed table alerts the office within a minute.",
      "An unchanged table sends nothing.",
      "One table makes one alert.",
      "The alert names the table’s own port.",
      "A failed send is retried three times.",
    ]);
    assert.deepEqual(textsOf(sectionOf(container, sectionNames[4]), ".tr[data-row] .strong"), [
      "A published table has one hash, seen in tables/publish.go.",
      "The office’s endpoint answers within five seconds.",
    ]);
    assert.deepEqual(textsOf(sectionOf(container, sectionNames[5]), ".exclusions li"), [
      "No SMS or email. The office asked for its endpoint only.",
      "No change to how tables are published. It is out of scope.",
    ]);
    assert.deepEqual(textsOf(sectionOf(container, sectionNames[6]), ".approach"), [
      "A watcher compares each published table’s hash on write and sends one alert per changed table to the office’s endpoint.",
    ]);
    assert.deepEqual(textsOf(sectionOf(container, sectionNames[6]), ".tr .strong"), ["Poll each table once a minute.", "Alert from the publisher itself."]);
    assert.deepEqual(textsOf(sectionOf(container, sectionNames[7]), ".tr .strong"), [
      "Merge the epic’s candidate into main, so the watcher ships.",
      "Turn alerts on for the harbour office, so they start receiving them.",
    ]);
    assert.deepEqual(textsOf(sectionOf(container, sectionNames[7]), ".inote"), [
      "Then: one real change to a table alerts the office once, within a minute.",
    ]);
    assert.deepEqual(textsOf(sectionOf(container, sectionNames[9]), ".checks"), ["go test ./alerts/...; then the full gate, just check."]);
  }));

test("ContractDoc: a contract with no additions says None under its heading", () =>
  withDoc(propsOf(tideProposed, { contract: create(EpicContractSchema, { ...fields, additions: [] }) }), (container) => {
    assert.deepEqual(textsOf(sectionOf(container, sectionNames[2]), ".inote"), ["None."]);
    assert.equal(sectionOf(container, sectionNames[2]).querySelectorAll(".tr").length, 0);
  }));

test("ContractDoc: markup in a field renders as text, never as elements", () => {
  const markup = '<img src="x" onerror="alert(1)"><b>bold</b>';
  return withDoc({ contract: create(EpicContractSchema, { ...fields, intent: markup }), requirements }, (container) => {
    assert.equal(textsOf(container, "article.contract .intent")[0], markup);
    assert.equal(container.querySelectorAll(".intent *").length, 0);
    assert.equal(container.querySelectorAll("img").length, 0);
  });
});

test("ContractDoc: nothing on the page is editable: no input, textarea or form", () =>
  withDoc(propsOf(tideProposed), (container) => {
    assert.equal(container.querySelectorAll("input, textarea, form").length, 0);
  }));

test("ContractMine: each open decision, its concerned row and the decisions section are marked attention", () =>
  withDoc(propsOf(tideProposed, { marks: marksOf(tideProposed, false) }), (container) => {
    assert.ok(one(container, '.tr[data-row="D1"]').classList.contains("attention"));
    assert.ok(one(container, '.tr[data-row="R1"]').classList.contains("attention"));
    assert.equal(one(container, '.tr[data-row="R2"]').classList.contains("attention"), false);
    assert.equal(one(container, '.tr[data-row="F1"]').classList.contains("attention"), false);
    const section = sectionOf(container, sectionNames[8]);
    assert.ok(one(section, ".sec").classList.contains("attention"));
    assert.equal(textsOf(section, ".sec .n")[0], "1");
  }));

test("ContractMine: an unread irreversible section is marked read this before Accept, and the ref is that section", () => {
  let drawn: HTMLElement | null = null;
  return withDoc(
    propsOf(tideProposed, { marks: marksOf(tideProposed, true), irreversibleRef: (element) => (drawn = element) }),
    (container) => {
      const section = sectionOf(container, sectionNames[7]);
      assert.ok(one(section, ".sec").classList.contains("attention"));
      assert.equal(textsOf(section, ".sec .unread")[0], "read this before Accept");
      assert.equal(drawn, section, "the ref names the irreversible section");
    },
  );
});

test("ContractMine: a read irreversible section is not marked", () =>
  withDoc(propsOf(tideProposed, { marks: marksOf(tideProposed, false) }), (container) => {
    const section = sectionOf(container, sectionNames[7]);
    assert.equal(one(section, ".sec").classList.contains("attention"), false);
    assert.equal(section.querySelectorAll(".unread").length, 0);
  }));

test("ContractMine: each row's control calls onAbout with the row's id and text", () => {
  const calls: RowAbout[] = [];
  const version = newestOf(tideProposed);
  return withDoc(propsOf(tideProposed, { onAbout: (about) => calls.push(about) }), (container) => {
    talkOf(container, "F1").click();
    talkOf(container, "R1").click();
    talkOf(container, "P1").click();
    talkOf(container, "D1").click();
    assert.deepEqual(calls, [
      { id: "F1", text: "The office was alerted about a table that had not changed." },
      { id: "R1", text: "A changed table alerts the office within a minute." },
      { id: "P1", text: "A published table has one hash, seen in tables/publish.go." },
      { id: "D1", text: version.decisions[0]?.question },
    ]);
  });
});

test("ContractMine: Keep and Strike call onAbout with the addition's location and Keep: or Strike: text", () => {
  const calls: RowAbout[] = [];
  return withDoc(propsOf(tideProposed, { onAbout: (about) => calls.push(about) }), (container) => {
    const [keep, strike] = all(container, ".keepdrop button");
    assert.equal(keep?.textContent, "Keep");
    assert.equal(strike?.textContent, "Strike");
    keep?.click();
    strike?.click();
    assert.deepEqual(calls, [
      { id: "alerts/send.go", text: "Keep: Retry a failed send three times." },
      { id: "alerts/send.go", text: "Strike: Retry a failed send three times." },
    ]);
  });
});

test("ContractMine: with no onAbout no row has a control, and the additions have no Keep or Strike", () =>
  withDoc(propsOf(tideProposed), (container) => {
    assert.equal(container.querySelectorAll("button").length, 0);
  }));

test("ContractVersions: the changed rows and sections carry a changed in v2 chip", () =>
  withDoc(propsOf(tideProposed, { marks: marksOf(tideProposed, false) }), (container) => {
    assert.deepEqual(
      all(container, ".tr[data-row]")
        .filter((row) => row.querySelector(".chip.new") !== null)
        .map((row) => row.getAttribute("data-row")),
      ["F2", "R3"],
    );
    assert.deepEqual(
      all(container, "article.contract > section[aria-label]")
        .filter((section) => section.querySelector(".sec .chip.new") !== null)
        .map((section) => section.getAttribute("aria-label")),
      [sectionNames[1], sectionNames[3]],
    );
    assert.deepEqual(textsOf(container, ".chip.new"), [
      "changed in v2",
      "changed in v2",
      "changed in v2",
      "changed in v2",
    ]);
  }));

test("ContractVersions: an open decision the version changed carries its changed in v2 chip", () => {
  const marks = { ...marksOf(tideProposed, false), changedRows: new Set(["D1"]) };
  return withDoc(propsOf(tideProposed, { marks }), (container) => {
    assert.equal(one(container, '.tr[data-row="D1"] .chip.new').textContent, "changed in v2");
  });
});

test("ContractVersions: with no marks nothing is marked changed", () =>
  withDoc(propsOf(tideProposed), (container) => {
    assert.equal(container.querySelectorAll(".chip.new").length, 0);
  }));

test("ContractMine: a section holding a row an open decision concerns is marked attention, as its row is", () =>
  withDoc(propsOf(tideProposed, { marks: marksOf(tideProposed, false) }), (container) => {
    assert.ok(one(sectionOf(container, sectionNames[3]), ".sec").classList.contains("attention"));
    assert.equal(one(sectionOf(container, sectionNames[1]), ".sec").classList.contains("attention"), false);
  }));

test("ContractMine: a body that is not attending draws no attention and says Decisions needed none, with a decision open", () =>
  withDoc(propsOf(tideSetAside, { attending: false, marks: marksOf(tideSetAside, true) }), (container) => {
    assert.equal(container.querySelectorAll(".attention").length, 0);
    assert.equal(container.querySelectorAll(".unread").length, 0);
    assert.equal(textsOf(container, ".dline .facts > span")[1], "Decisions needed: none");
    assert.equal(sectionOf(container, sectionNames[8]).querySelectorAll(".sec .n").length, 0);
  }));

test("ContractMine: the row control of the About is lit, and no other", () =>
  withDoc(propsOf(tideProposed, { onAbout: () => undefined, aboutId: "R2" }), (container) => {
    assert.deepEqual(
      all(container, ".say.on").map((say) => say.closest(".tr")?.getAttribute("data-row")),
      ["R2"],
    );
  }));

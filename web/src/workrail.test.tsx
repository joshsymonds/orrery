import assert from "node:assert/strict";
import { test } from "node:test";

import { create } from "@bufbuild/protobuf";
import { type JSX, render } from "preact";
import { act } from "preact/test-utils";

import { EpicRequirementSchema, EpicsGetResponseSchema } from "./gen/orrery/v1/epics_pb";
import { assertNone, assertSame, installTestDom, removeTestDom } from "./testdom";
import type { ContractThing, EpicThing, ListThing, RailGroup, Thing } from "./work";
import { WorkRail } from "./workrail";

// Each test renders WorkRail on its own, into a container on the document, so an event bubbles to the document as it
// does in the page. The fixture groups are literal RailGroup values, so each test names exactly the things it draws.

// withDom gives the check a container on the document, and removes the container and the DOM however the check ends.
async function withDom(check: (container: HTMLElement) => void): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.appendChild(container);
  try {
    check(container);
  } finally {
    render(null, container);
    container.remove();
    await removeTestDom(window);
  }
}

// draw renders the element into the container, and flushes its effects.
function draw(container: HTMLElement, element: JSX.Element): void {
  act(() => {
    render(element, container);
  });
}

// all returns the elements under root that match selector, in document order.
function all(root: ParentNode, selector: string): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(selector));
}

// one returns the first element under root that matches selector, which must be there.
function one(root: ParentNode, selector: string): HTMLElement {
  const found = root.querySelector<HTMLElement>(selector);
  assert.ok(found !== null, `no element matches ${selector}`);
  return found;
}

// textsOf returns the text of each element under root that matches selector.
function textsOf(root: ParentNode, selector: string): string[] {
  return all(root, selector).map((element) => element.textContent ?? "");
}

const noop = () => undefined;

// epicOf is a steered epic of the fixture, quiet and building, with no requirements, unless init says otherwise.
function epicOf(slug: string, init: Partial<EpicThing> = {}): EpicThing {
  return {
    kind: "epic",
    key: `epic:${slug}`,
    id: `widgets-5d0c1a7/${slug}`,
    slug,
    short: slug,
    asks: false,
    tone: "quiet",
    state: "building",
    detail: { requirements: [], centre: "" },
    ...init,
  };
}

// markOf is an epic's detail in centre, with one requirement per state in states, named R1, R2 and so on in order.
function markOf(centre: string, states: readonly string[]) {
  return create(EpicsGetResponseSchema, {
    centre,
    requirements: states.map((state, i) => create(EpicRequirementSchema, { id: `R${i + 1}`, state })),
  });
}

function agentsOf(count: number): ListThing {
  return { kind: "agents", key: "agents", count, tone: "live", tasks: [] };
}

function runningOf(count: number): ListThing {
  return { kind: "running", key: "running", count, tone: "quiet", tasks: [] };
}

// groupOf is a group of things, marked many the way workOf marks it.
function groupOf(kind: RailGroup["kind"], label: string, things: Thing[]): RailGroup {
  return { kind, label, things, many: things.length > 3 };
}

// epicsOf is a group of count quiet epics, named by the letters of the alphabet from a.
function epicsOf(count: number): RailGroup {
  const things = Array.from({ length: count }, (_, index) => epicOf(`epic-${"abcdefghi"[index]}`));
  return groupOf("epic", count === 1 ? "epic" : "epics", things);
}

// onRail draws groups on the desk's rail, lit at lit, opening its things into opened.
function onRail(groups: RailGroup[], lit = "", opened: Thing[] = []): JSX.Element {
  return <WorkRail groups={groups} lit={lit} layout="rail" onOpen={(thing) => opened.push(thing)} />;
}

test("WorkRail: groups draw in order, each name drawn once, in the rail", () => {
  const groups = [
    groupOf("epic", "epics", [
      epicOf("widgets-rail", { asks: true, tone: "attention" }),
      epicOf("orrery-picker", { tone: "live" }),
    ]),
    groupOf("agents", "agents", [agentsOf(2)]),
    groupOf("running", "running", [runningOf(1)]),
  ];
  return withDom((container) => {
    draw(container, onRail(groups));

    const rail = one(container, "aside.rail");
    assert.equal(rail.getAttribute("aria-label"), "what this conversation steers");
    assert.deepEqual(textsOf(rail, ".rgroup > .rlabel"), ["epics", "agents", "running"]);
    assert.deepEqual(
      all(rail, ".rgroup").map((element) => element.className),
      ["rgroup epic", "rgroup agents", "rgroup running"],
    );
  });
});

test("WorkRail: an epic tile draws its mark at 24, its short name, and ? when it asks", () => {
  const asking = epicOf("widgets-rail", {
    short: "rail",
    asks: true,
    tone: "attention",
    state: "rail asks: which store?",
    detail: markOf("asks", ["proven", "proven", "in_flight", "not_reached", "not_reached"]),
  });
  return withDom((container) => {
    draw(container, onRail([groupOf("epic", "epic", [asking])]));

    const tile = one(container, ".rgroup.epic .tile-w");
    assert.equal(one(tile, "svg.epicmark").getAttribute("data-size"), "24");
    assert.equal(one(tile, "svg.epicmark").getAttribute("aria-label"), "2 of 5 requirements proven, asks you a question");
    assert.equal(tile.querySelectorAll('svg.epicmark circle.rim[data-tone="ok"]').length, 2);
    assert.equal(tile.querySelectorAll('svg.epicmark circle.rim[data-tone="run"]').length, 1);
    assert.equal(one(tile, ".cap").textContent, "rail");
    assert.equal(one(tile, ".n").textContent, "?");
    assert.equal(one(tile, ".n").className, "n attention");
  });
});

test("WorkRail: an epic with no requirements says so in its mark, and one that does not ask draws no ?", () => {
  return withDom((container) => {
    draw(container, onRail([groupOf("epic", "epic", [epicOf("widgets-rail", { short: "rail" })])]));

    const tile = one(container, ".rgroup.epic .tile-w");
    assert.equal(one(tile, "svg.epicmark").getAttribute("aria-label"), "no requirements yet");
    assert.equal(tile.querySelector(".n"), null);
  });
});

test("WorkRail: the agents count is live, the running count is grey, and neither draws a short name", () => {
  return withDom((container) => {
    draw(
      container,
      onRail([groupOf("agents", "agents", [agentsOf(2)]), groupOf("running", "running", [runningOf(1)])]),
    );

    const agents = one(container, ".rgroup.agents .tile-w");
    assert.equal(one(agents, ".n").textContent, "2");
    assert.equal(one(agents, ".n").className, "n live");
    assert.equal(agents.querySelector(".cap"), null);

    const running = one(container, ".rgroup.running .tile-w");
    assert.equal(one(running, ".n").textContent, "1");
    assert.equal(one(running, ".n").className, "n quiet");
    assert.equal(running.querySelector(".cap"), null);
  });
});

test("WorkRail: the agents tile's face is the agent icon, with its count kept", () => {
  return withDom((container) => {
    draw(container, onRail([groupOf("agents", "agents", [agentsOf(2)])]));

    const face = one(container, ".rgroup.agents .tile");
    assert.equal(one(face, "svg.icon path").getAttribute("d"), "M8 2l1.5 4.5L14 8l-4.5 1.5L8 14l-1.5-4.5L2 8l4.5-1.5z");
    assert.equal(one(face, ".n").textContent, "2");
  });
});

test("WorkRail: the running tile's face is the shell icon, with its count kept", () => {
  return withDom((container) => {
    draw(container, onRail([groupOf("running", "running", [runningOf(1)])]));

    const face = one(container, ".rgroup.running .tile");
    assert.equal(one(face, "svg.icon path").getAttribute("d"), "M3 4l4 4-4 4M8.5 12H13");
    assert.equal(one(face, ".n").textContent, "1");
  });
});

test("WorkRail: no groups draw an empty rail", () => {
  return withDom((container) => {
    draw(container, onRail([]));

    const rail = one(container, "aside.rail");
    assert.equal(rail.children.length, 0);
  });
});

test("WorkRail: the lit tile is marked on with its tone and current, and no other tile is", () => {
  const groups = [
    groupOf("epic", "epics", [
      epicOf("widgets-rail", { short: "rail", asks: true, tone: "attention" }),
      epicOf("orrery-picker", { short: "picker", tone: "live" }),
    ]),
  ];
  return withDom((container) => {
    draw(container, onRail(groups, "epic:orrery-picker"));

    const lit = one(container, ".tile-w.on");
    assert.equal(lit.className, "tile-w on live");
    assert.equal(one(lit, ".tile").className, "tile live cur");
    assert.equal(one(lit, ".cap").textContent, "picker");
    assert.equal(all(container, ".tile-w.on").length, 1);
    assert.equal(all(container, ".tile.cur").length, 1);
  });
});

test("WorkRail: a rerender with an added steered epic draws its tile", () => {
  return withDom((container) => {
    draw(container, onRail([epicsOf(1)]));
    assert.equal(all(container, ".rgroup.epic .tile-w").length, 1);

    draw(container, onRail([epicsOf(2)]));
    assert.equal(all(container, ".rgroup.epic .tile-w").length, 2);
  });
});

test("RailPicker: three epics draw as three tiles, and four draw as one dots tile counting four", () => {
  return withDom((container) => {
    draw(container, onRail([epicsOf(3)]));
    assert.equal(all(container, ".rgroup.epic .tile-w").length, 3);
    assert.equal(all(container, ".tile-w.group").length, 0);

    draw(container, onRail([epicsOf(4)]));
    assert.equal(all(container, ".rgroup.epic .tile-w").length, 1);
    const dots = one(container, ".tile-w.group");
    assert.equal(one(dots, ".cap").textContent, "4");
    assert.equal(one(dots, ".n").textContent, "4");
    assert.equal(all(dots, ".dotgrid i").length, 4);
  });
});

test("RailPicker: a dots tile shows ? when one of its things asks", () => {
  const things = [
    epicOf("a", { asks: true, tone: "attention" }),
    epicOf("b"),
    epicOf("c"),
    epicOf("d"),
  ];
  return withDom((container) => {
    draw(container, onRail([groupOf("epic", "epics", things)]));

    const dots = one(container, ".tile-w.group");
    assert.equal(one(dots, ".n").textContent, "?");
    assert.equal(one(dots, ".cap").textContent, "4");
    assert.equal(one(dots, ".dotgrid i.attention").className, "attention");
  });
});

test("RailPicker: pressing the dots tile opens a picker of head and rows, focused on the first row", () => {
  return withDom((container) => {
    draw(container, onRail([epicsOf(4)]));
    assert.equal(container.querySelector(".rpick"), null);

    act(() => {
      one(container, ".tile-w.group").dispatchEvent(new Event("click", { bubbles: true }));
    });

    const picker = one(container, ".rpick");
    assert.equal(picker.getAttribute("role"), "menu");
    assert.equal(one(picker, ".menu-head").textContent, "4 epics");
    const rows = all(picker, "button.rpick-row[role=menuitem]");
    assert.deepEqual(rows.map((row) => one(row, ".t").textContent), ["epic-a", "epic-b", "epic-c", "epic-d"]);
    assert.equal(rows.length, 4);
    assertSame(document.activeElement, rows[0]);
  });
});

test("RailPicker: each picker row names its thing's state, an asking epic's by what it asks", () => {
  const things = [
    epicOf("epic-a", { state: "building" }),
    epicOf("epic-b", { state: "epic-b asks: which zone?", asks: true, tone: "attention" }),
    epicOf("epic-c", { state: "planning" }),
    epicOf("epic-d", { state: "released · 4m ago" }),
  ];
  return withDom((container) => {
    draw(container, onRail([groupOf("epic", "epics", things)]));
    act(() => {
      one(container, ".tile-w.group").dispatchEvent(new Event("click", { bubbles: true }));
    });

    assert.deepEqual(textsOf(one(container, ".rpick"), ".rpick-row .s"), [
      "building",
      "epic-b asks: which zone?",
      "planning",
      "released · 4m ago",
    ]);
  });
});

test("RailPicker: choosing a row calls onOpen with its thing and closes the picker", () => {
  const opened: Thing[] = [];
  const groups = [epicsOf(4)];
  return withDom((container) => {
    draw(container, <WorkRail groups={groups} lit="" layout="rail" onOpen={(thing) => opened.push(thing)} />);
    act(() => {
      one(container, ".tile-w.group").dispatchEvent(new Event("click", { bubbles: true }));
    });

    act(() => {
      all(container, "button.rpick-row")[2].dispatchEvent(new Event("click", { bubbles: true }));
    });

    assert.equal(opened.length, 1);
    assert.equal(opened[0], groups[0].things[2]);
    assert.equal(container.querySelector(".rpick"), null);
  });
});

test("RailPicker: Escape inside the picker closes it and never reaches the document", () => {
  let documentKeys = 0;
  const onDocumentKey = () => {
    documentKeys += 1;
  };
  return withDom((container) => {
    draw(container, onRail([epicsOf(4)]));
    act(() => {
      one(container, ".tile-w.group").dispatchEvent(new Event("click", { bubbles: true }));
    });
    document.addEventListener("keydown", onDocumentKey);
    try {
      const row = one(container, "button.rpick-row");
      act(() => {
        row.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      });
      assert.equal(container.querySelector(".rpick"), null);
      assert.equal(documentKeys, 0);
    } finally {
      document.removeEventListener("keydown", onDocumentKey);
    }
  });
});

test("RailPicker: Escape on the dots tile closes the picker and never reaches the document", () => {
  let documentKeys = 0;
  const onDocumentKey = () => {
    documentKeys += 1;
  };
  return withDom((container) => {
    draw(container, onRail([epicsOf(4)]));
    act(() => {
      one(container, ".tile-w.group").dispatchEvent(new Event("click", { bubbles: true }));
    });
    document.addEventListener("keydown", onDocumentKey);
    try {
      act(() => {
        one(container, ".tile-w.group").dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      });
      assertNone(container.querySelector(".rpick"));
      assert.equal(documentKeys, 0);
    } finally {
      document.removeEventListener("keydown", onDocumentKey);
    }
  });
});

test("RailPicker: Escape with focus on the page body closes the picker and never reaches the document", () => {
  let documentKeys = 0;
  const onDocumentKey = () => {
    documentKeys += 1;
  };
  return withDom((container) => {
    draw(container, onRail([epicsOf(4)]));
    act(() => {
      one(container, ".tile-w.group").dispatchEvent(new Event("click", { bubbles: true }));
    });
    document.addEventListener("keydown", onDocumentKey);
    try {
      act(() => {
        document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      });
      assertNone(container.querySelector(".rpick"));
      assert.equal(documentKeys, 0);
    } finally {
      document.removeEventListener("keydown", onDocumentKey);
    }
  });
});

test("RailPicker: once the picker is shut, Escape reaches the document again", () => {
  let documentKeys = 0;
  const onDocumentKey = () => {
    documentKeys += 1;
  };
  return withDom((container) => {
    draw(container, onRail([epicsOf(4)]));
    act(() => {
      one(container, ".tile-w.group").dispatchEvent(new Event("click", { bubbles: true }));
    });
    document.addEventListener("keydown", onDocumentKey);
    try {
      act(() => {
        document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      });
      act(() => {
        document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      });
      assert.equal(documentKeys, 1);
    } finally {
      document.removeEventListener("keydown", onDocumentKey);
    }
  });
});

test("RailPicker: a pointerdown outside the picker closes it, and one inside it does not", () => {
  return withDom((container) => {
    draw(container, onRail([epicsOf(4)]));
    act(() => {
      one(container, ".tile-w.group").dispatchEvent(new Event("click", { bubbles: true }));
    });

    act(() => {
      one(container, "button.rpick-row").dispatchEvent(new Event("pointerdown", { bubbles: true }));
    });
    assert.ok(container.querySelector(".rpick") !== null);

    act(() => {
      document.body.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    });
    assert.equal(container.querySelector(".rpick"), null);
  });
});

test("TileStrip: the strip draws the groups and their names, with end drawn last", () => {
  const groups = [
    groupOf("epic", "epic", [epicOf("widgets-rail", { short: "rail", asks: true, tone: "attention" })]),
    groupOf("agents", "agents", [agentsOf(2)]),
  ];
  const hide = <a class="fold-head">Hide the header</a>;
  return withDom((container) => {
    draw(container, <WorkRail groups={groups} lit="" layout="strip" onOpen={noop} end={hide} />);

    const strip = one(container, "nav.tilestrip");
    assert.equal(strip.getAttribute("aria-label"), "what this conversation steers");
    assert.deepEqual(textsOf(strip, ".rgroup > .rlabel"), ["epic", "agents"]);
    assert.equal(container.querySelector("aside.rail"), null);
    assert.equal(strip.lastElementChild?.className, "fold-head");
  });
});

test("TileStrip: the dots tile opens the picker as a sheet titled with its count and label", () => {
  const groups = [epicsOf(4)];
  return withDom((container) => {
    draw(container, <WorkRail groups={groups} lit="" layout="strip" onOpen={noop} />);
    act(() => {
      one(container, ".tile-w.group").dispatchEvent(new Event("click", { bubbles: true }));
    });

    const sheet = one(container, "section.sheet");
    assert.equal(sheet.getAttribute("role"), "dialog");
    assert.equal(sheet.getAttribute("aria-label"), "4 epics");
    assert.equal(one(sheet, ".rpick").getAttribute("role"), "menu");
    assert.equal(all(sheet, "button.rpick-row").length, 4);
  });
});

// contractOf is a proposed contract of the fixture, quiet of decisions, unless init says otherwise.
function contractOf(slug: string, init: Partial<ContractThing> = {}): ContractThing {
  return {
    kind: "contract",
    key: `contract:tidepool/${slug}`,
    id: `tidepool/${slug}`,
    slug,
    short: slug.split("-")[0],
    asks: false,
    tone: "attention",
    title: `${slug}: a contract waits for you`,
    ...init,
  };
}

const docIcon = "M4 2h5l3 3v9H4zM9 2v3h3M6 8h4M6 11h4";

test("ContractTile: a contract tile draws round 6's doc icon and its short name, and its title names the slug", () => {
  return withDom((container) => {
    draw(container, onRail([groupOf("contract", "contract", [contractOf("tide-alerts")])]));

    const tile = one(container, ".rgroup.contract .tile-w");
    assert.equal(tile.getAttribute("aria-label"), "tide-alerts: a contract waits for you");
    assert.equal(tile.getAttribute("title"), "tide-alerts: a contract waits for you");
    assert.equal(one(tile, ".cap").textContent, "tide");
    assert.equal(one(tile, "svg.icon path").getAttribute("d"), docIcon);
    assertNone(tile.querySelector("svg.epicmark"), "a contract tile draws no mark");
  });
});

test("ContractTile: a contract tile draws ? while it asks, and no mark once it asks nothing", () => {
  const things = [contractOf("tide-alerts", { asks: true }), contractOf("wave-report")];
  return withDom((container) => {
    draw(container, onRail([groupOf("contract", "contract", things)]));

    const [asking, settled] = all(container, ".rgroup.contract .tile-w");
    assert.equal(one(asking, ".n").textContent, "?");
    assert.equal(settled.querySelector(".n"), null);
  });
});

test("ContractTile: a picker of more than three contracts counts them, and lists each by its slug", () => {
  const things = ["alpha-one", "beta-two", "gamma-three", "delta-four"].map((slug) => contractOf(slug));
  return withDom((container) => {
    draw(container, onRail([groupOf("contract", "contract", things)]));

    const dots = one(container, ".tile-w.group");
    assert.equal(one(dots, ".n").textContent, "4");
    assert.equal(one(dots, ".cap").textContent, "4");

    act(() => {
      dots.dispatchEvent(new Event("click", { bubbles: true }));
    });

    const rows = all(container, "button.rpick-row[role=menuitem]");
    assert.deepEqual(
      rows.map((row) => one(row, ".t").textContent),
      ["alpha-one", "beta-two", "gamma-three", "delta-four"],
    );
    assert.deepEqual(
      rows.map((row) => one(row, ".s").textContent),
      Array(4).fill("a contract waits for you"),
    );
  });
});

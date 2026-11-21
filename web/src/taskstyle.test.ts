import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

// A rule of style.css: the selectors of its list, and its declarations by property, in source order. Comments are cut
// first. A rule inside an @media block is read as its own rule.
interface Rule {
  selectors: string[];
  declarations: Map<string, string>;
}

function withoutComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

function rulesOf(css: string): Rule[] {
  const rules: Rule[] = [];
  for (const match of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selectors = match[1]
      .split(",")
      .map((selector) => selector.trim().replace(/\s+/g, " "))
      .filter((selector) => selector !== "");
    const declarations = new Map<string, string>();
    for (const declaration of match[2].split(";")) {
      const colon = declaration.indexOf(":");
      if (colon > 0) {
        declarations.set(declaration.slice(0, colon).trim(), declaration.slice(colon + 1).trim());
      }
    }
    rules.push({ selectors, declarations });
  }
  return rules;
}

// rulesUnder is the rules inside every @media block of the query, such as "(width < 760px)", as the phone reads them.
function rulesUnder(css: string, query: string): Rule[] {
  const opener = `@media ${query} {`;
  const rules: Rule[] = [];
  let from = css.indexOf(opener);
  while (from !== -1) {
    const start = from + opener.length;
    let depth = 1;
    let end = start;
    while (depth > 0) {
      if (css[end] === "{") {
        depth += 1;
      } else if (css[end] === "}") {
        depth -= 1;
      }
      end += 1;
    }
    rules.push(...rulesOf(css.slice(start, end - 1)));
    from = css.indexOf(opener, end);
  }
  return rules;
}

// declaredValue is the value a property takes from the last rule that names the selector, or undefined when none does.
function declaredValue(rules: Rule[], selector: string, property: string): string | undefined {
  let value: string | undefined;
  for (const rule of rules) {
    const declared = rule.declarations.get(property);
    if (declared !== undefined && rule.selectors.includes(selector)) {
      value = declared;
    }
  }
  return value;
}

// deskOf is the stylesheet with its phone blocks cut out, so a desk value is never read from a phone rule that comes later.
function deskOf(css: string): string {
  const opener = "@media (width < 760px) {";
  let desk = css;
  let from = desk.indexOf(opener);
  while (from !== -1) {
    let depth = 1;
    let end = from + opener.length;
    while (depth > 0) {
      if (desk[end] === "{") {
        depth += 1;
      } else if (desk[end] === "}") {
        depth -= 1;
      }
      end += 1;
    }
    desk = desk.slice(0, from) + desk.slice(end);
    from = desk.indexOf(opener);
  }
  return desk;
}

const css = withoutComments(readFileSync(new URL("../src/style.css", import.meta.url), "utf8"));
const rules = rulesOf(css);
const desk = rulesOf(deskOf(css));
const phone = rulesUnder(css, "(width < 760px)");

// The rail a phase row takes is its border's colour. A passed row is ok; a now row takes its tone's role, as the
// task's tones read (run is live, mine held and attention, bad failed, acc accepted and engine).
test("TaskStyle: a passed phase row's rail is ok green", () => {
  assert.equal(declaredValue(desk, ".phase-output .phase.passed", "border-left-color"), "var(--o-role-ok)");
});

test("TaskStyle: a now phase row's rail is live while its task runs", () => {
  assert.equal(declaredValue(desk, ".phase-output .phase.now", "border-left-color"), "var(--o-role-live)");
});

test("TaskStyle: a held now phase row's rail is attention peach", () => {
  assert.equal(declaredValue(desk, ".phase-output .phase.now.mine", "border-left-color"), "var(--o-role-attention)");
});

test("TaskStyle: a failed now phase row's rail is bad red", () => {
  assert.equal(declaredValue(desk, ".phase-output .phase.now.bad", "border-left-color"), "var(--o-role-bad)");
});

test("TaskStyle: an accepted now phase row's rail is engine teal", () => {
  assert.equal(declaredValue(desk, ".phase-output .phase.now.acc", "border-left-color"), "var(--o-role-engine)");
});

test("TaskStyle: a limited now phase row's rail is grey, not held's peach", () => {
  assert.equal(declaredValue(desk, ".phase-output .phase.now.limited", "border-left-color"), "var(--o-ink-3)");
});

test("TaskStyle: an ahead phase row's head and name are greyed", () => {
  assert.equal(declaredValue(desk, ".phase-output .phase.ahead .phase-head", "color"), "var(--o-ink-4)");
  assert.equal(declaredValue(desk, ".phase-output .phase.ahead .phase-head .nm", "color"), "var(--o-ink-4)");
});

test("TaskStyle: a phase head holds one line and its summary is ellipsized", () => {
  assert.equal(declaredValue(desk, ".phase-output .phase-head .sum", "overflow"), "hidden");
  assert.equal(declaredValue(desk, ".phase-output .phase-head .sum", "text-overflow"), "ellipsis");
  assert.equal(declaredValue(desk, ".phase-output .phase-head .sum", "white-space"), "nowrap");
});

test("TaskStyle: a phase head's stage name does not wrap inside its word", () => {
  assert.equal(declaredValue(desk, ".phase-output .phase-head .nm", "white-space"), "nowrap");
});

test("TaskStyle: at phone width a phase head is a touch-height row", () => {
  assert.equal(declaredValue(phone, ".phase-output .phase-head", "min-height"), "var(--o-size-control-touch)");
});

test("TaskStyle: the epic pin is one flex line", () => {
  assert.equal(declaredValue(desk, ".epic-pin", "display"), "flex");
});

test("TaskStyle: the epic pin's words ellipsize on one line", () => {
  assert.equal(declaredValue(desk, ".epic-pin .words", "text-overflow"), "ellipsis");
  assert.equal(declaredValue(desk, ".epic-pin .words", "white-space"), "nowrap");
  assert.equal(declaredValue(desk, ".epic-pin .words", "overflow"), "hidden");
});

test("TaskStyle: the epic pin's blocker ellipsizes on one line and takes the attention role", () => {
  assert.equal(declaredValue(desk, ".epic-pin .blocker", "text-overflow"), "ellipsis");
  assert.equal(declaredValue(desk, ".epic-pin .blocker", "white-space"), "nowrap");
  assert.equal(declaredValue(desk, ".epic-pin .blocker", "color"), "var(--o-role-attention)");
});

test("TaskStyle: at phone width the epic pin's open-the-epic line is hidden", () => {
  assert.equal(declaredValue(phone, ".epic-pin .open", "display"), "none");
});

test("TaskStyle: the current task chip is raised and outlined", () => {
  assert.equal(declaredValue(desk, ".tchip.cur", "background"), "var(--o-surface-raised)");
  assert.ok(declaredValue(desk, ".tchip.cur", "box-shadow")?.includes("var(--o-role-focus)"));
});

test("TaskStyle: a task chip's label does not wrap", () => {
  assert.equal(declaredValue(desk, ".tchip", "white-space"), "nowrap");
});

test("TaskStyle: an attempt's check rounds sit in a reader of their own, one under the other, so each takes the block's rail", () => {
  assert.equal(declaredValue(desk, ".phase-output .phase-body .reader.check-rounds", "display"), "grid");
  assert.equal(declaredValue(desk, ".phase-output .phase-body .reader.check-rounds", "gap"), "var(--o-space-8)");
  assert.ok(declaredValue(desk, '.reader [data-shape="block"]', "border-left")?.includes("var(--o-size-rail)"));
});

test("TaskStyle: a check round's chip labels do not wrap", () => {
  assert.equal(declaredValue(desk, ".chip", "white-space"), "nowrap");
});

test("TaskStyle: a check round's chips take the ok role, and a failed chip the bad role", () => {
  assert.equal(declaredValue(desk, ".chip.ok", "color"), "var(--o-role-ok)");
  assert.equal(declaredValue(desk, ".chip.bad", "color"), "var(--o-role-bad)");
});

test("TaskStyle: an attempt's chip takes the ok, attention and accepted roles", () => {
  assert.equal(declaredValue(desk, ".chip.ok", "color"), "var(--o-role-ok)");
  assert.equal(declaredValue(desk, ".chip.attention", "color"), "var(--o-role-attention)");
  assert.equal(declaredValue(desk, ".chip.accepted", "color"), "var(--o-role-engine)");
});

test("TaskStyle: the planner's brief takes the engine rail", () => {
  assert.equal(declaredValue(desk, ".phase-output .brief", "border-left"), "var(--o-size-rail) solid var(--o-role-engine)");
});

test("TaskStyle: a stage's sub label is a grey label in the label type", () => {
  assert.equal(declaredValue(desk, ".phase-output .phase-body .sub", "color"), "var(--o-ink-3)");
  assert.equal(declaredValue(desk, ".phase-output .phase-body .sub", "font"), "var(--o-type-label)");
});

test("TaskStyle: the review table's desk columns are gate, finding, where and what happened", () => {
  assert.equal(declaredValue(desk, ".phase-output .tbl", "--cols"), "auto minmax(0, 1.4fr) auto minmax(0, 1fr)");
});

test("TaskStyle: at phone width the review table's rows stack in round 6's touch template", () => {
  assert.equal(declaredValue(phone, ".phase-output .tbl", "--cols-touch"), "auto minmax(0, 1fr)");
});

test("TaskStyle: at phone width the worker session's leading crumbs share one line and its page crumb takes the next", () => {
  assert.equal(declaredValue(phone, ".worker-session > .title-row .crumbs", "display"), "flex");
  assert.equal(declaredValue(phone, ".worker-session > .title-row .crumbs", "flex-wrap"), "wrap");
  assert.equal(declaredValue(phone, ".worker-session > .title-row .crumb.here", "flex"), "0 0 100%");
});

test("TaskStyle: at phone width the worker session's mark and the separator after it leave its title row", () => {
  assert.equal(declaredValue(phone, ".worker-session > .title-row .mark-crumb", "display"), "none");
  assert.equal(declaredValue(phone, ".worker-session > .title-row .mark-crumb + .sep", "display"), "none");
});

test("TaskStyle: at phone width a worker session's leading crumb ellipsizes on one line, and its page crumb wraps whole", () => {
  assert.equal(declaredValue(phone, ".worker-session > .title-row .crumb", "white-space"), "nowrap");
  assert.equal(declaredValue(phone, ".worker-session > .title-row .crumb", "text-overflow"), "ellipsis");
  assert.equal(declaredValue(phone, ".worker-session > .title-row .crumb.here", "white-space"), "normal");
  assert.equal(declaredValue(phone, ".worker-session > .title-row .crumb.here", "overflow-wrap"), "normal");
});

test("TaskStyle: the task's record lists, its evidence runs among them, sit flush with no list marks", () => {
  assert.equal(declaredValue(desk, ".phase-output ul", "padding"), "0");
  assert.equal(declaredValue(desk, ".phase-output ul", "list-style"), "none");
});

test("TaskStyle: at phone width the pin's state words go", () => {
  assert.equal(declaredValue(phone, ".epic-pin .words", "display"), "none");
});

test("TaskStyle: a step's end block keeps the reader's head row and summary rules, wherever a reader draws it", () => {
  assert.equal(declaredValue(desk, ".reader .step-card button.step-head", "display"), "flex");
  assert.equal(declaredValue(desk, ".reader .step-card button.step-head", "border"), "0");
  assert.equal(declaredValue(desk, ".reader .step-card .step-summary", "white-space"), "pre-wrap");
});

test("TaskStyle: at phone width the worker session's state line wraps, so its Stop confirm is not clipped", () => {
  assert.equal(declaredValue(phone, ".worker-session .statebar", "flex-wrap"), "wrap");
  assert.equal(declaredValue(phone, ".worker-session .statebar .end", "flex"), "1 1 auto");
  assert.equal(declaredValue(phone, ".worker-session .dock .confirm", "flex-wrap"), "wrap");
});

test("TaskStyle: a session door is a link line in the reference role, at phone width a touch row", () => {
  assert.equal(declaredValue(desk, ".phase-output .door > span:first-child", "color"), "var(--o-role-ref)");
  assert.equal(declaredValue(phone, ".phase-output .door", "min-height"), "var(--o-size-control-touch)");
});

test("TaskStyle: at phone width the foot bar's state word ellipsizes on one line", () => {
  assert.equal(declaredValue(desk, ".phase-output .cmdbar .state", "text-overflow"), "ellipsis");
  assert.equal(declaredValue(desk, ".phase-output .cmdbar .state", "white-space"), "nowrap");
});

// The phase page's old markup drew these classes; none draws them now, so no rule may name them.
const removedPhaseClasses = [
  "phase-row",
  "phases",
  "phase-name",
  "phase-summary",
  "phase-time",
  "phase-task",
  "task-title",
  "requirements",
  "notice",
  "round",
  "round-head",
  "round-no",
  "rounds",
  "check",
  "check-name",
  "check-detail",
  "checks",
  "gate-name",
  "gate-result",
  "gate-detail",
  "gate-stdout",
  "gate-stderr",
  "judge",
  "judge-verdict",
  "review",
  "review-head",
  "review-verdict",
  "reviews",
  "reviewer",
  "reviewers",
  "finding",
  "findings",
  "claim",
  "location",
  "reason",
  "result",
  "integration",
  "attempt-doing",
  "attempt-end",
  "attempt-text",
];

test("TaskStyle: no rule is left for the removed phase-row markup or the old phase page's classes", () => {
  const pattern = new RegExp(`\\.(${removedPhaseClasses.join("|")})(?![\\w-])`);
  const named = rules
    .flatMap((rule) => rule.selectors)
    .filter((selector) => selector.startsWith(".phase-output") && pattern.test(selector));
  assert.deepEqual(named, []);
});

test("TaskStyle: at the desk a worker's session drawn in the middle fills the reader's rows", () => {
  assert.equal(declaredValue(desk, ".read > .worker-session", "grid-row"), "1 / -1");
  assert.equal(declaredValue(desk, ".read > .worker-session", "min-height"), "0");
});

test("TaskStyle: a stage's line takes its tone: bad in the bad role, accepted in the accepted role the chips use", () => {
  assert.equal(declaredValue(desk, ".phase-output .inote.bad", "color"), "var(--o-role-bad)");
  assert.equal(declaredValue(desk, ".phase-output .inote.accepted", "color"), declaredValue(desk, ".chip.accepted", "color"));
});

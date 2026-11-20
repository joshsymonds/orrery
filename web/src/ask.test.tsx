import assert from "node:assert/strict";
import { test } from "node:test";

import type { JSX } from "preact";
import { render } from "preact";
import { act } from "preact/test-utils";

import { Ask, AskLine } from "./ask";
import type { AskModel, AskOption } from "./askmodel";
import { Icon, type IconName } from "./glyphs";
import { installTestDom, removeTestDom } from "./testdom";

type AskProps = Parameters<typeof Ask>[0];

// now is the instant every Ask in these tests is drawn at.
const now = 1_794_700_000;
const fourMinutes = 240;
const fiveDays = 5 * 86400;

const sessionOptions: AskOption[] = [
  { n: 1, name: "One query per week", does: "Join keepers in the week query.", recommended: true, why: "", answerable: true },
  { n: 2, name: "Cache keepers for the request", does: "Keeps the row code as it is.", recommended: false, why: "", answerable: true },
  { n: 3, name: "Paginate the page", does: "Twenty shifts a page.", recommended: false, why: "", answerable: true },
];

// epicOptions are an epic's choices. The first is recommended, so it starts picked.
const epicOptions: AskOption[] = [
  {
    n: 1,
    name: "partial",
    does: "Add a partial index on open shifts only.",
    recommended: true,
    why: "open shifts are the only rows the backfill scans",
    answerable: true,
  },
  { n: 2, name: "composite", does: "Add a composite index on keeper and shift.", recommended: false, why: "", answerable: true },
  { n: 3, name: "none", does: "Leave the table as it is.", recommended: false, why: "", answerable: true },
];

// sessionModel is a headless session's open question, which has waited four minutes and has an archive target.
function sessionModel(init: Partial<AskModel> = {}): AskModel {
  return {
    kind: "session",
    who: "tidepool · Why the roster page is slow",
    asked: now - fourMinutes,
    question: "Which fix should I make?",
    command: null,
    options: sessionOptions,
    putOff: false,
    archive: { kind: "headless", id: "s-1" },
    target: { kind: "headless", id: "s-1", requestId: "r-1", options: sessionOptions.map((option) => option.name) },
    epic: null,
    ...init,
  };
}

// epicModel is an epic's question, asked four minutes before now.
function epicModel(init: Partial<AskModel> = {}): AskModel {
  return {
    kind: "epic",
    who: "lighthouse · rota-export",
    asked: now - fourMinutes,
    question: "Which index should the backfill add?",
    command: null,
    options: epicOptions,
    putOff: false,
    archive: null,
    target: null,
    epic: { id: "E1", questionId: "Q2" },
    ...init,
  };
}

// parkModel is an epic's park, with its one answer that runs the parked work again.
function parkModel(): AskModel {
  return {
    kind: "park",
    who: "lighthouse · rota-export",
    asked: now - fourMinutes,
    question: "waiting on the harbour office",
    command: null,
    options: [{ n: 1, name: "Retry", does: "Run the parked work again", recommended: false, why: "", answerable: true }],
    putOff: false,
    archive: null,
    target: null,
    epic: { id: "E1", questionId: "" },
  };
}

function baseProps(init: Partial<AskProps>): AskProps {
  return {
    model: sessionModel(),
    now,
    phase: "open",
    picked: null,
    note: "",
    keyCaps: true,
    narrow: false,
    numberKeys: false,
    ...init,
  };
}

function epicProps(init: Partial<AskProps>): AskProps {
  return baseProps({ model: epicModel(), ...init });
}

// withTree renders a tree into a fresh container, runs check on it, and removes it.
async function withTree(tree: JSX.Element, check: (container: HTMLElement) => Promise<void>): Promise<void> {
  const window = installTestDom();
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => {
      render(tree, container);
    });
    await check(container);
  } finally {
    await act(() => {
      render(null, container);
    });
    container.remove();
    await removeTestDom(window);
  }
}

async function withAsk(init: Partial<AskProps>, check: (container: HTMLElement) => Promise<void>): Promise<void> {
  return withTree(<Ask {...baseProps(init)} />, check);
}

// settle lets a pending answer resolve and its state update render.
async function settle(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

// findButton returns the first button whose accessible name, or else its text, starts with text.
function findButton(root: ParentNode, text: string): HTMLButtonElement | undefined {
  return [...root.querySelectorAll<HTMLButtonElement>("button")].find((button) =>
    (button.getAttribute("aria-label") ?? button.textContent ?? "").startsWith(text),
  );
}

// press clicks the button that findButton finds for text, and fails when no button is drawn for it.
async function press(root: ParentNode, text: string): Promise<void> {
  const button = findButton(root, text);
  assert.ok(button, `a button "${text}" is drawn`);
  await act(() => {
    button.click();
  });
}

// options are the option cells of the question in order: each choice, then Something else when the question has it.
function options(root: ParentNode): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(".opts > .opt")];
}

// pickedOptions are the positions, from 0, of the options that hold the picked ring.
function pickedOptions(root: ParentNode): number[] {
  return options(root).flatMap((option, index) => (option.classList.contains("picked") ? [index] : []));
}

// clickOption clicks option n, counted from 1 as the keys number them.
async function clickOption(root: ParentNode, n: number): Promise<void> {
  const option = options(root)[n - 1];
  assert.ok(option, `option ${n} is drawn`);
  await act(() => {
    option.click();
  });
}

// pressKey sends a key to the document, as the keyboard does when no field has focus.
async function pressKey(key: string): Promise<void> {
  await act(() => {
    document.dispatchEvent(new KeyboardEvent("keydown", { key, cancelable: true }));
  });
}

// typeInto sets the answer box's text as a typist does: its value, then its input event.
async function typeInto(root: ParentNode, text: string): Promise<void> {
  const box = root.querySelector<HTMLTextAreaElement>('textarea[aria-label="answer"]');
  assert.ok(box, "the answer box is drawn");
  await act(() => {
    box.value = text;
    box.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

function textOf(root: ParentNode, selector: string): string | null {
  return root.querySelector(selector)?.textContent ?? null;
}

test("AskAnswer: the question's web address is drawn as a link, as the reader draws a reply", () =>
  withAsk({ model: sessionModel({ question: "Should I follow https://example.com/runbook first?" }) }, async (container) => {
    const link = container.querySelector<HTMLAnchorElement>(".ask .q a");
    assert.ok(link, "the address is not drawn as a link inside .ask .q");
    assert.equal(link.getAttribute("href"), "https://example.com/runbook");
    assert.equal(container.querySelector(".ask .q")?.textContent, "Should I follow https://example.com/runbook first?");
  }));

test("AskAnswer: a tap answers its own option once", () => {
  const answers: number[] = [];
  return withAsk(
    {
      onAnswer: (n) => {
        answers.push(n);
      },
    },
    async (container) => {
      assert.equal(options(container).length, 3, "the three options are not all drawn");
      await clickOption(container, 2);
      assert.deepEqual(answers, [2], "a tap on option 2 did not answer option 2 once");
    },
  );
});

test("AskAnswer: sending rings the pick, ignores presses, and draws no Not now or More", () => {
  const answers: number[] = [];
  return withAsk(
    {
      phase: "sending",
      picked: 2,
      onAnswer: (n) => {
        answers.push(n);
      },
      onNotNow: () => {},
      onDecline: () => {},
    },
    async (container) => {
      assert.deepEqual(pickedOptions(container), [1], "the picked option does not hold the ring");
      assert.equal(textOf(container, ".foot .inote"), "sending…");
      assert.equal(findButton(container, "Not now"), undefined, "Not now is drawn while sending");
      assert.equal(findButton(container, "More for this question"), undefined, "More is drawn while sending");
      await clickOption(container, 1);
      await clickOption(container, 3);
      assert.deepEqual(answers, [], "a press sent an answer while one was in flight");
    },
  );
});

test("AskAnswer: a refused answer draws its note under the options, and the options answer again", () => {
  const answers: number[] = [];
  return withAsk(
    {
      phase: "error",
      note: "Not answered: the question changed before your answer arrived. This is what it asks now.",
      onAnswer: (n) => {
        answers.push(n);
      },
    },
    async (container) => {
      assert.equal(
        textOf(container, ".inote.error"),
        "Not answered: the question changed before your answer arrived. This is what it asks now.",
      );
      await clickOption(container, 1);
      assert.deepEqual(answers, [1], "the Ask did not stay open after a refused answer");
    },
  );
});

test("AskAnswer: an epic answers only on confirm, showing the record line", () => {
  const answers: [string, string][] = [];
  return withTree(
    <Ask
      {...epicProps({
        onConfirm: (choice, text) => {
          answers.push([choice, text]);
          return Promise.resolve("");
        },
      })}
    />,
    async (container) => {
      await clickOption(container, 2);
      assert.deepEqual(answers, [], "a pick answered the epic's question");
      assert.equal(textOf(container, ".record code"), "owner: Add a composite index on keeper and shift.");
      await press(container, "Answer with composite");
      await settle();
      assert.deepEqual(answers, [["composite", "Add a composite index on keeper and shift."]]);
    },
  );
});

test("AskAnswer: while an epic answer is in flight its pick shows sending and presses send nothing", () => {
  const answers: string[] = [];
  let finish: (words: string) => void = () => {};
  return withTree(
    <Ask
      {...epicProps({
        onConfirm: (choice) => {
          answers.push(choice);
          return new Promise((resolve) => {
            finish = resolve;
          });
        },
      })}
    />,
    async (container) => {
      await press(container, "Answer with partial");
      assert.equal(textOf(container, ".foot .inote"), "sending…");
      await clickOption(container, 2);
      assert.deepEqual(pickedOptions(container), [0], "a press during the answer moved the pick");
      assert.deepEqual(answers, ["partial"], "a press during the answer sent another answer");
      await act(() => {
        finish("");
      });
      await settle();
      assert.equal(textOf(container, ".foot .inote"), null, "sending is still drawn once the answer is recorded");
    },
  );
});

test("AskAnswer: an epic's refused answer shows its words, and a failed one shows the fixed line; both keep the pick", () => {
  let attempts = 0;
  return withTree(
    <Ask
      {...epicProps({
        onConfirm: () => {
          attempts++;
          return attempts === 1 ? Promise.resolve("question closed") : Promise.reject(new Error("upstream said: secret-7"));
        },
      })}
    />,
    async (container) => {
      await clickOption(container, 2);
      await press(container, "Answer with composite");
      await settle();
      assert.equal(textOf(container, ".inote.error"), "question closed");
      assert.deepEqual(pickedOptions(container), [1], "the refused pick was lost");
      await press(container, "Answer with composite");
      await settle();
      assert.equal(textOf(container, ".inote.error"), "Not answered. Your answer is kept.");
      assert.equal(container.textContent?.includes("secret-7"), false, "server text is shown");
      assert.deepEqual(pickedOptions(container), [1], "the failed pick was lost");
    },
  );
});

test("AskAnswer: Something else sends other with the typed text, once it has some", () => {
  const answers: [string, string][] = [];
  return withTree(
    <Ask
      {...epicProps({
        onConfirm: (choice, text) => {
          answers.push([choice, text]);
          return Promise.resolve("");
        },
      })}
    />,
    async (container) => {
      await clickOption(container, 4);
      assert.ok(container.querySelector('textarea[aria-label="answer"]'), "picking Something else draws no text box");
      assert.ok(findButton(container, "Answer with Something else")?.disabled, "confirm is enabled while the text is blank");
      await typeInto(container, "   ");
      assert.ok(findButton(container, "Answer with Something else")?.disabled, "a blank text enables confirm");
      await typeInto(container, "  Use main  ");
      await press(container, "Answer with Something else");
      await settle();
      assert.deepEqual(answers, [["other", "Use main"]]);
    },
  );
});

test("AskAnswer: an epic's Something else is a role button that holds its text box, and a key picks it", () =>
  withTree(
    <Ask
      {...epicProps({
        onConfirm: () => Promise.resolve(""),
      })}
    />,
    async (container) => {
      const other = options(container)[3];
      assert.ok(other, "Something else is not the fourth option");
      assert.equal(other.tagName, "DIV", "Something else is not a div.opt");
      assert.equal(other.getAttribute("role"), "button", "Something else does not have role button");
      assert.equal(other.getAttribute("tabindex"), "0", "Something else cannot take focus");
      assert.equal(container.querySelector(".opts + textarea"), null, "the text box is drawn after the options");
      await act(() => {
        other.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
      });
      assert.deepEqual(pickedOptions(container), [3], "Enter on Something else did not pick it");
      await clickOption(container, 1);
      await act(() => {
        other.dispatchEvent(new KeyboardEvent("keydown", { key: " ", bubbles: true, cancelable: true }));
      });
      assert.deepEqual(pickedOptions(container), [3], "Space on Something else did not pick it");
      const box = options(container)[3]?.querySelector('.field.area textarea[aria-label="answer"]');
      assert.ok(box, "the text box is not drawn inside Something else");
      const enter = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
      await act(() => {
        box.dispatchEvent(enter);
      });
      assert.equal(enter.defaultPrevented, false, "Enter in the text box was taken by Something else");
    },
  ));

test("AskAnswer: a park answers with Retry on confirm, with no Something else and no record", () => {
  const answers: [string, string][] = [];
  return withTree(
    <Ask
      {...baseProps({
        model: parkModel(),
        onConfirm: (choice, text) => {
          answers.push([choice, text]);
          return Promise.resolve("");
        },
      })}
    />,
    async (container) => {
      assert.equal(options(container).length, 1, "a park draws more than its one answer");
      assert.equal(container.querySelector(".record"), null, "a park draws a record line");
      await clickOption(container, 1);
      await press(container, "Answer with Retry");
      await settle();
      assert.deepEqual(answers, [["Retry", "Run the parked work again"]]);
    },
  );
});

test("AskAnswer: on an epic's page digits 1 to 9 pick and never confirm", () => {
  const answers: string[] = [];
  return withTree(
    <Ask
      {...epicProps({
        numberKeys: true,
        onConfirm: (choice) => {
          answers.push(choice);
          return Promise.resolve("");
        },
      })}
    />,
    async (container) => {
      await pressKey("2");
      assert.deepEqual(pickedOptions(container), [1], "digit 2 did not pick option 2");
      await pressKey("4");
      assert.deepEqual(pickedOptions(container), [3], "digit 4 did not pick Something else");
      await pressKey("9");
      assert.deepEqual(pickedOptions(container), [3], "a digit past the last option moved the pick");
      assert.deepEqual(answers, [], "a digit confirmed an answer");
    },
  );
});

test("AskAnswer: keyCaps false draws no caps, and digits pick nothing when numberKeys is off", () =>
  withTree(<Ask {...epicProps({ keyCaps: false, numberKeys: false })} />, async (container) => {
    assert.equal(container.querySelector("kbd"), null, "a key cap is drawn with keyCaps false");
    await pressKey("2");
    assert.deepEqual(pickedOptions(container), [0], "a digit picked an option inside a transcript");
  }));

test("AskAnswer: key caps draw at the desk and not under 760 px", async () => {
  await withAsk({}, async (container) => {
    assert.equal(textOf(container, ".opts > .opt kbd"), "1", "the desk draws no key cap on option 1");
  });
  await withAsk({ narrow: true }, async (container) => {
    assert.equal(container.querySelector("kbd"), null, "a key cap is drawn under 760 px");
  });
});

test("AskAnswer: the answered line folds to one line, with its key cap at the desk only", async () => {
  await withTree(
    <AskLine state="answered" n={1} name="One query per week" question="Which fix should I make?" keyCaps />,
    async (container) => {
      assert.equal(textOf(container, ".ask.folded .lbl"), "You answered");
      assert.equal(textOf(container, ".ask.folded kbd"), "1");
      assert.equal(textOf(container, ".ask.folded"), "You answered1One query per week· Which fix should I make?");
    },
  );
  await withTree(
    <AskLine state="answered" n={1} name="One query per week" question="Which fix should I make?" keyCaps={false} />,
    async (container) => {
      assert.equal(container.querySelector("kbd"), null, "the answered line draws a key cap with keyCaps false");
    },
  );
});

test("AskAnswer: the settled line names its question's project and slug", () =>
  withTree(<AskLine state="settled" who="lighthouse · rota-export" />, async (container) => {
    assert.equal(textOf(container, ".ask.folded.settled .lbl"), "Settled");
    assert.equal(textOf(container, ".ask.folded.settled"), "SettledThis question is settled.· lighthouse · rota-export");
  }));

test("NotNow: Not now and Bring it back call their callbacks", async () => {
  let notNows = 0;
  let backs = 0;
  await withAsk(
    {
      onNotNow: () => {
        notNows++;
      },
      onBringBack: () => {
        backs++;
      },
    },
    async (container) => {
      await press(container, "Not now");
      assert.equal(notNows, 1, "Not now did not call onNotNow once");
      assert.equal(backs, 0, "Not now called onBringBack");
    },
  );
  await withAsk(
    {
      model: sessionModel({ putOff: true }),
      onNotNow: () => {
        notNows++;
      },
      onBringBack: () => {
        backs++;
      },
    },
    async (container) => {
      await press(container, "Bring it back");
      assert.equal(backs, 1, "Bring it back did not call onBringBack once");
      assert.equal(notNows, 1, "Bring it back called onNotNow");
    },
  );
});

// iconPaths are the path data of the drawing in root, in order.
function iconPaths(root: ParentNode): string[] {
  return [...root.querySelectorAll("svg path")].map((path) => path.getAttribute("d") ?? "");
}

// buttonIcon is the drawing of the button that findButton finds for text.
function buttonIcon(root: ParentNode, text: string): string[] {
  const button = findButton(root, text);
  assert.ok(button, `a button "${text}" is drawn`);
  return iconPaths(button);
}

// glyphPaths is the drawing of a glyph as the web's Icon draws it, which a control's icon must match.
function glyphPaths(name: IconName): string[] {
  const drawing = document.createElement("div");
  render(<Icon name={name} />, drawing);
  return iconPaths(drawing);
}

test("NotNow: put off draws quiet with its asked age and no stale foot, and an open question draws its age", async () => {
  await withAsk({ model: sessionModel({ putOff: true }) }, async (container) => {
    assert.ok(container.querySelector(".ask.later"), "a put-off question is not drawn quiet");
    assert.equal(textOf(container, ".head .lbl"), "Put off");
    assert.equal(container.querySelectorAll(".age").length, 1, "a put-off question does not draw exactly one age");
    assert.equal(textOf(container, ".age"), "· 4m ago", "a put-off question does not draw its asked age");
  });
  await withAsk({ model: sessionModel({ putOff: true, asked: now - fiveDays }) }, async (container) => {
    assert.equal(container.querySelector(".foot"), null, "a put-off question draws a stale foot at five days");
  });
  await withAsk({}, async (container) => {
    assert.equal(container.querySelector(".ask.later"), null, "an open question is drawn quiet");
    assert.equal(textOf(container, ".head .lbl"), "Asks you");
    assert.equal(textOf(container, ".age"), "· 4m ago");
  });
});

test("NotNow: each control draws its icon: Not now the clock, Bring it back the retry, Archive this session the archive", async () => {
  await withAsk({ onNotNow: () => {} }, async (container) => {
    assert.deepEqual(buttonIcon(container, "Not now"), glyphPaths("clock"), "Not now does not draw the clock icon");
  });
  await withAsk({ model: sessionModel({ putOff: true }), onBringBack: () => {} }, async (container) => {
    assert.deepEqual(buttonIcon(container, "Bring it back"), glyphPaths("retry"), "Bring it back does not draw the retry icon");
  });
  await withAsk({ model: sessionModel({ asked: now - fiveDays }), onArchive: () => {} }, async (container) => {
    assert.deepEqual(
      buttonIcon(container, "Archive this session"),
      glyphPaths("archive"),
      "Archive this session does not draw the archive icon",
    );
  });
});

test("NotNow: an epic draws neither Not now nor Bring it back", () =>
  withTree(
    <Ask
      {...epicProps({
        onNotNow: () => {},
        onBringBack: () => {},
      })}
    />,
    async (container) => {
      assert.equal(findButton(container, "Not now"), undefined, "an epic's question draws Not now");
      assert.equal(findButton(container, "Bring it back"), undefined, "an epic's question draws Bring it back");
    },
  ));

test("Decline: only inside More, a Sheet when narrow", async () => {
  let declines = 0;
  const onDecline = () => {
    declines++;
  };
  await withAsk({ onDecline }, async (container) => {
    assert.equal(findButton(container, "Decline and end the turn"), undefined, "Decline is drawn before More is pressed");
    await press(container, "More for this question");
    assert.equal(container.querySelector("[role='dialog']"), null, "More at the desk is drawn as a Sheet");
    await press(container, "Decline and end the turn");
    assert.equal(declines, 1, "Decline did not call onDecline once");
  });
  declines = 0;
  await withAsk({ onDecline, narrow: true }, async (container) => {
    await press(container, "More for this question");
    const sheet = container.querySelector("[role='dialog']");
    assert.ok(sheet, "under 760 px More is not a Sheet");
    assert.ok(findButton(sheet, "Decline and end the turn"), "the Sheet does not hold Decline");
    await act(() => {
      findButton(sheet, "Decline and end the turn")?.click();
    });
    assert.equal(declines, 1, "Decline in the Sheet did not call onDecline once");
  });
});

test("Decline: its item is the danger item that denies the request", () =>
  withAsk({ onDecline: () => {} }, async (container) => {
    await press(container, "More for this question");
    const decline = findButton(container, "Decline and end the turn");
    assert.ok(decline, "More does not draw Decline");
    assert.equal(decline.className, "menu-item danger", "Decline is not the danger item");
    assert.equal(decline.querySelector(".sub")?.textContent, "denies the request", "Decline does not say it denies the request");
  }));

test("Decline: none on an epic", () =>
  withTree(
    <Ask
      {...epicProps({
        onDecline: () => {},
      })}
    />,
    async (container) => {
      assert.equal(findButton(container, "More for this question"), undefined, "an epic's question draws More");
      assert.equal(findButton(container, "Decline and end the turn"), undefined, "an epic's question draws Decline");
    },
  ));

test("Decline: Archive this session only with an archive target", async () => {
  let archives = 0;
  await withAsk(
    {
      model: sessionModel({ asked: now - fiveDays }),
      onArchive: () => {
        archives++;
      },
    },
    async (container) => {
      assert.equal(textOf(container, ".foot .inote"), "Asked 5 days ago and still waiting.");
      await press(container, "Archive this session");
      assert.equal(archives, 1, "Archive this session did not call onArchive once");
    },
  );
  await withAsk(
    {
      model: sessionModel({ asked: now - fiveDays, archive: null }),
      onArchive: () => {
        archives++;
      },
    },
    async (container) => {
      assert.equal(findButton(container, "Archive this session"), undefined, "a pane with no orrery id draws Archive");
      assert.equal(archives, 1);
    },
  );
});

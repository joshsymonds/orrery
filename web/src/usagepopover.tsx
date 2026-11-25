// The usage popover: every account's windows at reading size, in the band's columns and order. Each window cell adds its
// countdown to the reset, with the reset's clock in its tip. Under each name go who uses the account, else its plan, or
// when it is limited, when its limit lifts; then when it was read. An "Accounts and routes" link calls onAccounts. Escape,
// and a press outside the popover, call onClose. The band draws it beside the band, and the collapsed rail beside the rail.
// Under the accounts, when spend is given, a foot totals the seven days' spend: what billed, each seat that billed with its
// amount, and the subscription traffic priced at list. A null spend, which is what a page holds before a Spend read lands,
// draws no foot.
// When use is given, a row offers Use where some context names another account, and "in use" where every context that
// names an account names this one. Use asks in its row which contexts move and to which account; Move makes the moves
// through moveContexts. A move that did not finish says in its row what moved and what did not, or that it could not tell.
// A move that set every context calls onDone, and every move calls onRead, so the page reads the routes again.

import { create } from "@bufbuild/protobuf";
import type { JSX } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

import { nothingChanged } from "./accountswrite";
import { type Account, type RouteRegistry, RouteRegistrySchema } from "./gen/orrery/v1/accounts_pb";
import { ageText } from "./inboxrow";
import { windowClaims } from "./usagedata";
import {
  type BandRow,
  dollarsText,
  movingContexts,
  type SpendTotals,
  type UseOffer,
  useOffer,
  type WindowDraw,
} from "./usagemodel";
import { type Move, type MoveResult, moveContexts, type RoutesClient } from "./usageuse";

// windowHues are the hue classes of the window cells, in the order of windowClaims: 5h, 7d, and Fable for 7d_oi.
const windowHues = ["d5", "d7", "fb"] as const;

const secondsPerMinute = 60;
const secondsPerHour = 3600;
const secondsPerDay = 86400;

// clockFormat is the 24-hour clock of the viewer's zone, two digits each side of the colon.
const clockFormat = new Intl.DateTimeFormat("en-GB", { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });

// dayFormat is the short weekday of a day, for a reset that falls on another day.
const dayFormat = new Intl.DateTimeFormat("en-GB", { weekday: "short" });

// noRegistry is the registry a move is checked against when the page holds none yet. No context is on it, so a move made
// against it sets nothing.
const noRegistry: RouteRegistry = create(RouteRegistrySchema);

// moveVerbs and undoVerbs name what landed and what did not, in the words a move and an undo say.
const moveVerbs = { landed: "Moved", notLanded: "Not moved" };
const undoVerbs = { landed: "Moved back", notLanded: "Not moved back" };

// untilText is how long from now until the time at, in the words the popover uses: "3d", "2h 20m", "2h", "45m". A time that
// has passed reads as none left.
function untilText(at: number, now: number): string {
  const seconds = Math.max(0, at - now);
  const days = Math.floor(seconds / secondsPerDay);
  const hours = Math.floor((seconds % secondsPerDay) / secondsPerHour);
  const minutes = Math.floor((seconds % secondsPerHour) / secondsPerMinute);
  if (days > 0) {
    return `${days}d`;
  }
  if (hours > 0) {
    return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`;
  }
  return `${minutes}m`;
}

// resetTip is a window's reset as its tip reads: its clock, with the weekday when the reset is not on today's date.
function resetTip(at: number, now: number): string {
  const reset = new Date(at * 1000);
  const clock = clockFormat.format(reset);
  return reset.toDateString() === new Date(now * 1000).toDateString()
    ? `resets ${clock}`
    : `resets ${dayFormat.format(reset)} ${clock}`;
}

// whoText is what sits under a row's name: who uses the account, else when its limit lifts while it is limited, else its plan.
function whoText(row: BandRow): string {
  if (row.inUse) {
    return `used by ${row.usedBy.join(", ")}`;
  }
  if (row.limited) {
    return row.liftsAt === null ? "limited" : `limited until ${clockFormat.format(new Date(row.liftsAt * 1000))}`;
  }
  return row.account.plan;
}

// nameOf is the name of the account with id, or the id itself when no account holds it.
function nameOf(accounts: readonly Account[], id: string): string {
  return accounts.find((account) => account.id === id)?.name ?? id;
}

// listText joins names in words: "a", "a and b", "a, b and c".
function listText(names: readonly string[]): string {
  if (names.length < 2) {
    return names.join("");
  }
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

// sentence ends text with a full stop, unless it already ends with a mark.
function sentence(text: string): string {
  return /[.?!]$/.test(text) ? text : `${text}.`;
}

// askWords is the question a Use asks in its row: which contexts move, and to which account. When they come from more than
// one account, each names its source, since the question assumes no single one. Their sessions send their next request from
// the account.
function askWords(plan: readonly Move[], account: string, accounts: readonly Account[]): string {
  const sources = new Set(plan.map((move) => move.from));
  const named = plan.map((move) =>
    sources.size > 1 ? `${move.context} from ${nameOf(accounts, move.from)}` : move.context,
  );
  return `Move ${listText(named)} to ${nameOf(accounts, account)}? Their sessions send their next request from it.`;
}

// movedWords is the toast's words for a move that set its contexts: the contexts, and the account they now use.
export function movedWords(moves: readonly Move[], account: string, accounts: readonly Account[]): string {
  return `Moved ${listText(moves.map((move) => move.context))} to ${nameOf(accounts, account)}.`;
}

// endWords is the words of a move or an undo that did not finish, in the place it shows. A move that stopped part way says
// what landed, what did not and why; where nothing landed, it says what each context that did not still uses. An unknown
// one says it could not tell. target is the account the move set, which an undo sets its contexts away from.
export function endWords(
  result: Exclude<MoveResult, { kind: "done" }>,
  undo: boolean,
  target: string,
  accounts: readonly Account[],
): string {
  if (result.kind === "unknown") {
    return result.reason;
  }
  const verbs = undo ? undoVerbs : moveVerbs;
  const reason = sentence(result.reason);
  if (result.moved.length > 0) {
    const notLanded =
      result.notMoved.length > 0 ? ` ${verbs.notLanded}: ${listText(result.notMoved.map((move) => move.context))}.` : "";
    return `${verbs.landed} ${listText(result.moved.map((move) => move.context))}.${notLanded} ${reason}`;
  }
  const stays = result.notMoved.map(
    (move) => `${move.context} still uses ${nameOf(accounts, undo ? target : move.from)}`,
  );
  return `${verbs.notLanded}: ${reason} ${sentence(stays.join("; "))}`;
}

// UseCalls is what offers Use on the popover's rows. routes is the client a move writes through. registry is the registry the
// page holds, which a move checks the contexts against at the moment it is made. onDone is called when a move has set every
// context it planned, with those contexts and the account they now use. onRead is called when a move ends, however it ended,
// so the page reads the routes again.
export interface UseCalls {
  routes: RoutesClient;
  registry: RouteRegistry | undefined;
  onDone: (moves: readonly Move[], account: string) => void;
  onRead: () => void;
}

// UseState is what Use shows of an account: nothing; the question asked in its row, with the contexts it plans to move and
// whether the move is under way; or the words a move ended with, in its row.
export type UseState =
  | { kind: "none" }
  | { kind: "asking"; account: string; plan: readonly Move[]; moving: boolean }
  | { kind: "words"; account: string; words: string };

// RowUse is what a row draws of Use: its offer, the question it asks or the words it shows, whether a move is under way, and
// its presses. While a move is under way, no Use, Cancel or Move is pressed.
interface RowUse {
  offer: UseOffer | null;
  question: string | null;
  busy: boolean;
  words: string | null;
  onAsk: () => void;
  onCancel: () => void;
  onMove: () => void;
}

// WindowCell draws one window of a row: its value over a bar, and its countdown to the reset. A window no current reading
// carries reads as no reading.
function WindowCell({ draw, hue, now }: { draw: WindowDraw | null; hue: (typeof windowHues)[number]; now: number }): JSX.Element {
  if (draw === null || draw.percent === null) {
    return (
      <span class="up-win">
        <span class="up-v">no reading</span>
      </span>
    );
  }
  const level = draw.level === null ? "" : ` level-${draw.level}`;
  const width = Math.min(Math.max(draw.percent, 0), 100);
  return (
    <span class={`up-win ${hue}${level}`} title={draw.reset === null ? undefined : resetTip(draw.reset, now)}>
      <span class="up-v">{`${draw.percent}%`}</span>
      <span class="up-bar">
        <i style={{ width: `${width}%` }} />
      </span>
      <span class="up-r">{draw.reset === null ? "" : `in ${untilText(draw.reset, now)}`}</span>
    </span>
  );
}

// AccountRow draws one account: its name, what sits under it, when it was read, its Use, and its three windows. A question or
// the words of a move sit under the row, across its columns.
function AccountRow({ row, now, use }: { row: BandRow; now: number; use: RowUse | null }): JSX.Element {
  const who = whoText(row);
  return (
    <div class={row.inUse ? "up-row inuse" : "up-row"} data-account={row.account.id}>
      <div class="up-acct">
        <span class="up-nm">{row.account.name}</span>
        {who !== "" && <span class="up-who">{who}</span>}
        {row.readAt !== null && (
          <span class={row.stale === null ? "up-read" : "up-read stale"}>{`read ${ageText(row.readAt, now)} ago`}</span>
        )}
        {use?.offer === "use" && (
          <button type="button" class="up-use" disabled={use.busy} onClick={use.onAsk}>
            Use
          </button>
        )}
        {use?.offer === "in use" && <span class="up-inuse">in use</span>}
      </div>
      {row.windows.map((draw, index) => (
        <WindowCell key={windowClaims[index]} draw={draw} hue={windowHues[index]} now={now} />
      ))}
      {use !== null && use.question !== null && (
        <div class="up-ask">
          <span class="up-q">{use.question}</span>
          <button type="button" class="btn quiet" disabled={use.busy} onClick={use.onCancel}>
            <span>Cancel</span>
          </button>
          <button type="button" class="btn primary" disabled={use.busy} onClick={use.onMove}>
            <span>Move</span>
          </button>
        </div>
      )}
      {use !== null && use.words !== null && (
        <div class="up-note" role="status">
          {use.words}
        </div>
      )}
    </div>
  );
}

// UsagePopover draws every row: the band's account names and windows at reading size, with a header over the columns and
// the link to the accounts and routes page, then the spend foot when spend is given. Escape, and a press outside it, call
// onClose. initialUse is the Use state the popover opens with; the stories draw a question or words with it, and the page
// gives none.
export function UsagePopover({
  rows,
  now,
  spend,
  use,
  initialUse,
  onAccounts,
  onClose,
}: {
  rows: readonly BandRow[];
  now: number;
  spend?: SpendTotals | null;
  use?: UseCalls;
  initialUse?: UseState;
  onAccounts: () => void;
  onClose: () => void;
}): JSX.Element {
  const root = useRef<HTMLDivElement>(null);
  const [pending, setPending] = useState<UseState>(initialUse ?? { kind: "none" });
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };
    const onPress = (event: Event) => {
      const target = event.target;
      const inside = target instanceof Node && root.current !== null && root.current.contains(target);
      if (!inside) {
        onClose();
      }
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPress);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPress);
    };
  }, [onClose]);

  const accounts = rows.map((row) => row.account);

  // moveTo makes the moves a question planned, to account, checked against the registry the page holds now. It says how the
  // move ended in the row. A move that sets every context calls onDone; every move calls onRead.
  async function moveTo(calls: UseCalls, account: string, plan: readonly Move[]): Promise<void> {
    setPending({ kind: "asking", account, plan, moving: true });
    const result = await moveContexts(calls.routes, plan, account, calls.registry ?? noRegistry);
    calls.onRead();
    if (result.kind === "done" && result.moved.length > 0) {
      setPending({ kind: "none" });
      calls.onDone(result.moved, account);
      return;
    }
    setPending({
      kind: "words",
      account,
      words:
        result.kind === "done"
          ? `Not moved: the contexts changed since this was asked. ${nothingChanged}`
          : endWords(result, false, account, accounts),
    });
  }

  // rowUse is what the row of an account draws of Use, or null when the popover is given no calls to move with.
  const rowUse = (row: BandRow): RowUse | null => {
    if (use === undefined) {
      return null;
    }
    const calls = use;
    const id = row.account.id;
    const asked = pending.kind === "asking" && pending.account === id ? pending : null;
    const words = pending.kind === "words" && pending.account === id ? pending.words : null;
    return {
      offer: useOffer(id, calls.registry),
      question: asked === null ? null : askWords(asked.plan, id, accounts),
      busy: pending.kind === "asking" && pending.moving,
      words,
      onAsk: () => setPending({ kind: "asking", account: id, plan: movingContexts(calls.registry, id), moving: false }),
      onCancel: () => setPending({ kind: "none" }),
      onMove: () => {
        if (asked !== null) {
          void moveTo(calls, id, asked.plan);
        }
      },
    };
  };

  // A press in the popover is not a press on the band, which would open it again, so the popover stops it here.
  return (
    <div class="upop" role="dialog" aria-label="Claude accounts" ref={root} onClick={(event) => event.stopPropagation()}>
      <div class="up-head">
        <h3 class="up-title">Claude accounts</h3>
        <a
          class="up-link"
          href="#/accounts"
          onClick={(event) => {
            event.preventDefault();
            onAccounts();
          }}
        >
          Accounts and routes
        </a>
      </div>
      <div class="up-table">
        <span class="up-hd nm">account</span>
        <span class="up-hd c5">5h</span>
        <span class="up-hd c7">7d</span>
        <span class="up-hd cf">Fable</span>
        {rows.map((row) => (
          <AccountRow key={row.account.id} row={row} now={now} use={rowUse(row)} />
        ))}
      </div>
      {spend && <SpendFoot spend={spend} />}
    </div>
  );
}

// SpendFoot draws the popover's foot: the billed total over the seven days, a line for each seat that billed with its
// amount, and the subscription traffic at list price.
function SpendFoot({ spend }: { spend: SpendTotals }): JSX.Element {
  return (
    <div class="up-foot">
      <FootLine label="billed, last 7 days" amount={spend.billed} />
      {spend.seats.map((seat) => (
        <FootLine key={seat.seat} label={seat.seat} amount={seat.billed} />
      ))}
      <FootLine label="subscription traffic, at list price" amount={spend.subscriptionList} />
    </div>
  );
}

// FootLine draws one line of the foot: its label, and its amount in nano US dollars drawn in dollars.
function FootLine({ label, amount }: { label: string; amount: bigint }): JSX.Element {
  return (
    <div class="up-frow">
      <span class="up-lbl">{label}</span>
      <span class="up-v">{dollarsText(amount)}</span>
    </div>
  );
}

// The clock of the fixture slices: the instant they are set at, the notation their times are written in, and the commit
// ids the epics name. "@13:50" is that time on 2026-11-14 UTC, and "@-2d 14:41" the same time two days earlier.

// liveEpoch is the instant the live browser starts at. The browser runs in UTC.
export const liveEpoch = "2026-11-14T14:41:00Z";

const day0 = 1794614400n; // 2026-11-14T00:00:00Z

// at parses the time notation to unix seconds: "@HH:MM[:SS]" or "@-Nd HH:MM[:SS]".
export function at(notation: string): bigint {
  const match = /^@(?:(-\d+)d )?(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(notation);
  if (match === null) {
    throw new Error(`fixtures: not a time notation: ${notation}`);
  }
  const [, days = "0", hh = "0", mm = "0", ss = "0"] = match;
  return day0 + BigInt(days) * 86400n + BigInt(Number(hh) * 3600 + Number(mm) * 60 + Number(ss));
}

// The commits the epics name, by their full ids.
export const commit = {
  rotaBase: "3e1f2ab6b681479f7e6dc3c139194a1ad6b8025b",
  rotaRtrunk: "a41b7d0a994984ee328f27586ece2fe411cb3a73",
  rotaCsv: "3f9c2e1d1ebb1664a69ab497310da60951dcb4be",
  rotaHandler: "d84a2c96e1f0b3a75c2d9e04f6a81b3c7d5e9f20",
  rotaExport: "7c1e9aae44b1b9c7c44bf9fc9e373fe744a0778d",
  rotaRetry: "e02b6c4eb44d9510b3215d974e1e03e5ada58a06",
  holderBase: "0c7e4aa22f3584d2a720b837a5507c36476852fe",
  holderScope: "b91e1edfa49dfc460d8c98ff0e6685a01712e19b",
  holderCap: "984ef49c3bff6d10661e3548cdb3e04724b8873c",
  holderNotify: "9cc2be1e8289c469a9f34e48ecabac2bcd4516d3",
  holderDocs: "9593e0b35d9438987e69889b8d9899fbc42bcaaa",
} as const;

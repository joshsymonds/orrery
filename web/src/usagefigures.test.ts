import assert from "node:assert/strict";
import { test } from "node:test";

import { create, type MessageInitShape } from "@bufbuild/protobuf";

import { type UsageSessionTotal, UsageSessionTotalSchema } from "./gen/orrery/v1/usage_pb";
import { dollars, kindsView, modelRows, sessionSum, tokens } from "./usagefigures";

// total builds one session total from the fields a test names: every other field is zero or empty, so a test reads only
// what it sets.
function total(init: MessageInitShape<typeof UsageSessionTotalSchema>): UsageSessionTotal {
  return create(UsageSessionTotalSchema, init);
}

test("UsageFigures", async (t) => {
  await t.test("dollars writes nano US dollars as dollars to two decimals", () => {
    assert.equal(dollars(0n), "$0.00");
    assert.equal(dollars(1_000_000_000n), "$1.00");
    assert.equal(dollars(1_230_000_000n), "$1.23");
    assert.equal(dollars(3_820_000_000n), "$3.82");
  });

  await t.test("tokens writes a count under a million as whole thousands", () => {
    assert.equal(tokens(0n), "0k");
    assert.equal(tokens(1_000n), "1k");
    assert.equal(tokens(999_000n), "999k");
  });

  await t.test("tokens writes a count from a million to under ten million as one decimal of millions", () => {
    assert.equal(tokens(1_000_000n), "1.0M");
    assert.equal(tokens(1_500_000n), "1.5M");
    assert.equal(tokens(9_900_000n), "9.9M");
  });

  await t.test("tokens writes a count from ten million as whole millions", () => {
    assert.equal(tokens(10_000_000n), "10M");
    assert.equal(tokens(48_000_000n), "48M");
  });

  await t.test("sessionSum adds each total's requests, costs and four token kinds", () => {
    const sum = sessionSum([
      total({
        requests: 3n,
        inputTokens: 10n,
        outputTokens: 20n,
        cacheReadTokens: 30n,
        cacheWriteTokens: 40n,
        billedCostNanoUsd: 5n,
        listCostNanoUsd: 7n,
      }),
      total({
        requests: 4n,
        inputTokens: 1n,
        outputTokens: 2n,
        cacheReadTokens: 3n,
        cacheWriteTokens: 4n,
        billedCostNanoUsd: 6n,
        listCostNanoUsd: 8n,
      }),
    ]);
    assert.deepEqual(sum, {
      requests: 7n,
      billed: 11n,
      list: 15n,
      inputTokens: 11n,
      outputTokens: 22n,
      cacheReadTokens: 33n,
      cacheWriteTokens: 44n,
    });
  });

  await t.test("kindsView carries each kind's dollars when every total sets all four kind costs and they sum to the list cost", () => {
    const view = kindsView([
      total({
        inputTokens: 10n,
        listInputNanoUsd: 1n,
        outputTokens: 20n,
        listOutputNanoUsd: 2n,
        cacheReadTokens: 30n,
        listCacheReadNanoUsd: 3n,
        cacheWriteTokens: 40n,
        listCacheWriteNanoUsd: 4n,
        listCostNanoUsd: 10n,
      }),
      total({
        inputTokens: 1n,
        listInputNanoUsd: 5n,
        outputTokens: 2n,
        listOutputNanoUsd: 6n,
        cacheReadTokens: 3n,
        listCacheReadNanoUsd: 7n,
        cacheWriteTokens: 4n,
        listCacheWriteNanoUsd: 8n,
        listCostNanoUsd: 26n,
      }),
    ]);
    assert.equal(view.hasDollars, true);
    assert.deepEqual(
      view.kinds.map((kind) => [kind.kind, kind.label, kind.tokens, kind.dollars]),
      [
        ["input", "input", 11n, 6n],
        ["output", "output", 22n, 8n],
        ["cacheRead", "cache read", 33n, 10n],
        ["cacheWrite", "cache write", 44n, 12n],
      ],
    );
  });

  await t.test("kindsView carries no dollars when one total leaves a kind cost unset, though the rest sum to the list cost", () => {
    const view = kindsView([
      total({
        inputTokens: 10n,
        listInputNanoUsd: 1n,
        outputTokens: 20n,
        listOutputNanoUsd: 2n,
        cacheReadTokens: 30n,
        listCacheReadNanoUsd: 3n,
        cacheWriteTokens: 40n,
        listCostNanoUsd: 6n,
      }),
    ]);
    assert.equal(view.hasDollars, false);
    assert.deepEqual(
      view.kinds.map((kind) => kind.dollars),
      [undefined, undefined, undefined, undefined],
      "a kind carries dollars although a kind cost is unset",
    );
  });

  await t.test("kindsView carries no dollars when the kind costs do not sum to the list cost", () => {
    const view = kindsView([
      total({
        inputTokens: 10n,
        listInputNanoUsd: 1n,
        outputTokens: 20n,
        listOutputNanoUsd: 2n,
        cacheReadTokens: 30n,
        listCacheReadNanoUsd: 3n,
        cacheWriteTokens: 40n,
        listCacheWriteNanoUsd: 4n,
        listCostNanoUsd: 11n,
      }),
    ]);
    assert.equal(view.hasDollars, false);
    assert.deepEqual(
      view.kinds.map((kind) => kind.dollars),
      [undefined, undefined, undefined, undefined],
      "a kind carries dollars although the kinds do not sum to the list cost",
    );
  });

  await t.test("modelRows reads model and agent, main for an empty agent type, largest list first, ties by model then agent", () => {
    const rows = modelRows([
      total({ model: "sonnet-5-5", agentType: "", listCostNanoUsd: 100n, billedCostNanoUsd: 90n }),
      total({ model: "opus-5-5", agentType: "steelman", listCostNanoUsd: 100n, billedCostNanoUsd: 0n }),
      total({ model: "opus-5-5", agentType: "", listCostNanoUsd: 100n, billedCostNanoUsd: 50n }),
      total({ model: "haiku-5-5", agentType: "Explore", listCostNanoUsd: 300n, billedCostNanoUsd: 10n }),
    ]);
    assert.deepEqual(
      rows.map((row) => [row.model, row.agent, row.billed, row.list]),
      [
        ["haiku-5-5", "Explore", 10n, 300n],
        ["opus-5-5", "main", 50n, 100n],
        ["opus-5-5", "steelman", 0n, 100n],
        ["sonnet-5-5", "main", 90n, 100n],
      ],
    );
  });

  await t.test("modelRows gives each row its share of the session's list cost", () => {
    const rows = modelRows([
      total({ model: "haiku-5-5", agentType: "Explore", listCostNanoUsd: 300n }),
      total({ model: "opus-5-5", agentType: "", listCostNanoUsd: 100n }),
      total({ model: "sonnet-5-5", agentType: "", listCostNanoUsd: 100n }),
      total({ model: "fable-5-2", agentType: "", listCostNanoUsd: 100n }),
    ]);
    assert.deepEqual(
      rows.map((row) => [row.model, row.share]),
      [
        ["haiku-5-5", 300 / 600],
        ["fable-5-2", 100 / 600],
        ["opus-5-5", 100 / 600],
        ["sonnet-5-5", 100 / 600],
      ],
    );
  });

  await t.test("modelRows gives each row a share of zero when no total costs anything", () => {
    const rows = modelRows([total({ model: "opus-5-5", agentType: "", requests: 2n })]);
    assert.deepEqual(
      rows.map((row) => row.share),
      [0],
    );
  });
});

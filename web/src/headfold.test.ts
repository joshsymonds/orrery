import assert from "node:assert/strict";
import { test } from "node:test";

import { foldOf, localHeadStore, memoryHeadStore } from "./headfold";

const headKey = "orrery.head.hidden";

// fakeStorage is an in-memory Storage with the three methods the store uses.
function fakeStorage(): Pick<Storage, "getItem" | "setItem" | "removeItem"> & { values: Map<string, string> } {
  const values = new Map<string, string>();
  return {
    values,
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => {
      values.set(key, value);
    },
    removeItem: (key) => {
      values.delete(key);
    },
  };
}

// throwingStorage throws from each method, as a blocked or cleared site storage does.
function throwingStorage(): Pick<Storage, "getItem" | "setItem" | "removeItem"> {
  const fail = (): never => {
    throw new Error("storage blocked");
  };
  return { getItem: fail, setItem: fail, removeItem: fail };
}

test("PhoneHead: foldOf is open when the layout is not narrow, whatever else holds", () => {
  for (const hidden of [false, true]) {
    for (const atNewest of [false, true]) {
      for (const tapped of [false, true]) {
        assert.equal(foldOf(false, hidden, atNewest, tapped), "open");
      }
    }
  }
});

test("PhoneHead: foldOf is hidden when the owner hid the header, narrow, whatever the reading position", () => {
  for (const atNewest of [false, true]) {
    for (const tapped of [false, true]) {
      assert.equal(foldOf(true, true, atNewest, tapped), "hidden");
    }
  }
});

test("PhoneHead: foldOf is reading when narrow, not hidden, scrolled off the newest line and not tapped", () => {
  assert.equal(foldOf(true, false, false, false), "reading");
});

test("PhoneHead: foldOf is open at the newest line when narrow and not hidden", () => {
  assert.equal(foldOf(true, false, true, false), "open");
});

test("PhoneHead: foldOf is open after a tap while scrolled off the newest line", () => {
  assert.equal(foldOf(true, false, false, true), "open");
});

test("PhoneHead: localHeadStore writes the key as 1 when hidden and removes it when shown", () => {
  const storage = fakeStorage();
  const store = localHeadStore(storage);

  store.setHidden(true);
  assert.equal(storage.values.get(headKey), "1");

  store.setHidden(false);
  assert.equal(storage.values.has(headKey), false);
});

test("PhoneHead: localHeadStore reads back a hidden header from a second store over the same storage, a reload", () => {
  const storage = fakeStorage();
  localHeadStore(storage).setHidden(true);

  assert.equal(localHeadStore(storage).hidden(), true);

  localHeadStore(storage).setHidden(false);
  assert.equal(localHeadStore(storage).hidden(), false);
});

test("PhoneHead: localHeadStore over null storage reads false and drops writes without throwing", () => {
  const store = localHeadStore(null);

  assert.equal(store.hidden(), false);
  assert.doesNotThrow(() => store.setHidden(true));
  assert.equal(store.hidden(), false);
});

test("PhoneHead: localHeadStore over throwing storage reads false and drops writes without throwing", () => {
  const store = localHeadStore(throwingStorage());

  assert.equal(store.hidden(), false);
  assert.doesNotThrow(() => store.setHidden(true));
  assert.doesNotThrow(() => store.setHidden(false));
  assert.equal(store.hidden(), false);
});

test("PhoneHead: memoryHeadStore starts at its initial value and keeps the value it is set to", () => {
  assert.equal(memoryHeadStore().hidden(), false);
  assert.equal(memoryHeadStore(true).hidden(), true);

  const store = memoryHeadStore(true);
  store.setHidden(false);
  assert.equal(store.hidden(), false);
  store.setHidden(true);
  assert.equal(store.hidden(), true);
});

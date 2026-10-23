// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Small, volatile stale-while-revalidate store.
 *
 * Nothing is persisted: records disappear on reload and on an authentication
 * change. Callers choose explicit scope keys and projections, so data from one
 * Project/Generation cannot be shown in another and sensitive fields need not
 * enter the cache. A monotonically increasing request id makes the newest
 * request the only one allowed to replace a snapshot.
 */
window.AnvilUI = window.AnvilUI || {};

(function () {
  const stores = new Set();

  function clone(value) {
    if (typeof structuredClone === "function") return structuredClone(value);
    return JSON.parse(JSON.stringify(value));
  }

  function createStore({ maxEntries = 8, maxAgeMs = 10 * 60 * 1000, now = () => Date.now() } = {}) {
    const entries = new Map();
    const requests = new Map();
    let epoch = 0;

    function peek(key) {
      const entry = entries.get(String(key));
      if (!entry) return null;
      if (now() - entry.updatedAt > maxAgeMs) {
        entries.delete(String(key));
        return null;
      }
      // LRU touch. The returned value is detached from the cached snapshot.
      entries.delete(String(key));
      entries.set(String(key), entry);
      return { data: clone(entry.data), updatedAt: entry.updatedAt };
    }

    async function revalidate(key, loader, { project = (value) => value } = {}) {
      key = String(key);
      const request = (requests.get(key) || 0) + 1;
      requests.set(key, request);
      const startedEpoch = epoch;
      const value = await loader();
      if (startedEpoch !== epoch || requests.get(key) !== request) {
        return { accepted: false, data: value };
      }
      const data = clone(project(value));
      const updatedAt = now();
      // Build the complete entry before publishing it: readers see old or new,
      // never a partly updated object.
      entries.delete(key);
      entries.set(key, { data, updatedAt });
      while (entries.size > maxEntries) entries.delete(entries.keys().next().value);
      return {
        accepted: true,
        // The live caller may need fields that policy excludes from a stale
        // snapshot. `snapshot` is exactly what was retained.
        data: clone(value),
        snapshot: clone(data),
        updatedAt,
      };
    }

    function invalidate(key) {
      if (key == null) {
        epoch++;
        entries.clear();
        requests.clear();
        return;
      }
      key = String(key);
      entries.delete(key);
      requests.set(key, (requests.get(key) || 0) + 1);
    }

    const store = { peek, revalidate, invalidate, get size() { return entries.size; } };
    stores.add(store);
    return store;
  }

  window.AnvilUI.swr = {
    createStore,
    clearAll() {
      for (const store of stores) store.invalidate();
    },
    ageLabel(updatedAt, now = Date.now()) {
      if (!updatedAt) return "never";
      const seconds = Math.max(0, Math.floor((now - updatedAt) / 1000));
      if (seconds < 5) return "just now";
      if (seconds < 60) return `${seconds}s ago`;
      const minutes = Math.floor(seconds / 60);
      return minutes < 60 ? `${minutes}m ago` : `${Math.floor(minutes / 60)}h ago`;
    },
  };
})();

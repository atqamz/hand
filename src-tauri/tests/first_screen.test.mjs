import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";

const script = readFileSync(new URL("../../web/main.js", import.meta.url), "utf8");

function load(handlers) {
  const elements = {};
  const documentListeners = {};
  const seen = {};
  const timers = [];
  const invoked = [];
  const element = () => ({
    hidden: false,
    disabled: false,
    textContent: "",
    value: "",
    listeners: {},
    addEventListener(type, listener) {
      this.listeners[type] = listener;
    },
  });
  const document = {
    visibilityState: "visible",
    getElementById(id) {
      elements[id] ??= element();
      if (id === "intro" && !(id in seen)) elements[id].hidden = seen[id] = true;
      return elements[id];
    },
    addEventListener(type, listener) {
      documentListeners[type] = listener;
    },
  };
  const window = {
    location: { href: "tauri://localhost/", search: "", pathname: "/" },
    __TAURI__: {
      core: {
        invoke(command) {
          invoked.push(command);
          return handlers[command]();
        },
      },
    },
  };
  const context = vm.createContext({
    window,
    document,
    history: { replaceState() {} },
    URLSearchParams,
    setTimeout(fn, ms) {
      timers.push({ fn, ms, live: true });
      return timers.length - 1;
    },
    clearTimeout(id) {
      if (timers[id]) timers[id].live = false;
    },
  });
  vm.runInContext(script, context);
  return { elements, documentListeners, timers, invoked, window, document };
}

const settle = () => new Promise((resolve) => setImmediate(resolve));
const unreachable = (retry = true) =>
  Promise.reject({ message: "Can't reach 4d92…6ed6.", retry });

test("a saved laptop that cannot be reached shows its name and Retry, never the form", async () => {
  const page = load({
    intake_intent: async () => null,
    open_board: () => unreachable(),
  });
  await settle();
  assert.equal(page.elements.paste.hidden, true);
  assert.equal(page.elements.unreachable.hidden, false);
  assert.equal(page.elements["unreachable-message"].textContent, "Can't reach 4d92…6ed6.");
  assert.equal(page.elements.retry.disabled, false);
});

test("a reload asks open_board again and keeps the message", async () => {
  const handlers = {
    intake_intent: async () => null,
    open_board: () => unreachable(),
  };
  const first = load(handlers);
  await settle();
  const second = load(handlers);
  await settle();
  assert.equal(first.invoked.filter((c) => c === "open_board").length, 1);
  assert.equal(second.elements["unreachable-message"].textContent, "Can't reach 4d92…6ed6.");
  assert.equal(second.elements.paste.hidden, true);
});

test("Retry reruns open_board and opens the Board when the laptop answers", async () => {
  let answer = unreachable;
  const page = load({
    intake_intent: async () => null,
    open_board: () => answer(),
  });
  await settle();
  answer = async () => "http://localhost:49152/";
  page.elements.retry.listeners.click();
  await settle();
  assert.equal(page.window.location.href, "http://localhost:49152/");
});

test("automatic retries back off, double and stop at 30 seconds", async () => {
  const page = load({
    intake_intent: async () => null,
    open_board: () => unreachable(),
  });
  await settle();
  for (let i = 0; i < 7; i++) {
    const next = page.timers.filter((t) => t.live).at(-1);
    next.live = false;
    next.fn();
    await settle();
  }
  assert.deepEqual(
    page.timers.map((t) => t.ms),
    [1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000],
  );
});

test("no automatic retry in the background, one on return to the foreground", async () => {
  const page = load({
    intake_intent: async () => null,
    open_board: () => unreachable(),
  });
  await settle();
  page.document.visibilityState = "hidden";
  page.documentListeners.visibilitychange();
  assert.equal(page.timers.filter((t) => t.live).length, 0);
  page.document.visibilityState = "visible";
  page.documentListeners.visibilitychange();
  await settle();
  assert.equal(page.invoked.filter((c) => c === "open_board").length, 2);
});

test("a failure that cannot succeed by waiting does not retry on its own", async () => {
  const page = load({
    intake_intent: async () => null,
    open_board: () => unreachable(false),
  });
  await settle();
  assert.equal(page.timers.filter((t) => t.live).length, 0);
  assert.equal(page.elements.unreachable.hidden, false);
});

test("Pair again shows the form and stops the retries", async () => {
  const page = load({
    intake_intent: async () => null,
    open_board: () => unreachable(),
  });
  await settle();
  page.elements.again.listeners.click();
  assert.equal(page.elements.paste.hidden, false);
  assert.equal(page.elements.unreachable.hidden, true);
  assert.equal(page.timers.filter((t) => t.live).length, 0);
});

test("an unpaired phone gets the form", async () => {
  const page = load({
    intake_intent: async () => null,
    open_board: async () => null,
  });
  await settle();
  assert.equal(page.elements.paste.hidden, false);
  assert.equal(page.elements.unreachable.hidden, true);
});

function pending() {
  let settle;
  const promise = new Promise((resolve, reject) => {
    settle = { resolve, reject };
  });
  return { promise, ...settle };
}

test("the pairing heading stays hidden for a saved laptop and shows Connecting… during the first dial", async () => {
  const dial = pending();
  const page = load({
    intake_intent: async () => null,
    open_board: () => dial.promise,
  });
  await settle();
  assert.equal(page.elements.intro.hidden, true);
  assert.equal(page.elements.status.textContent, "Connecting…");
  dial.reject({ message: "Can't reach 4d92…6ed6.", retry: true });
  await settle();
  assert.equal(page.elements.intro.hidden, true);
  assert.equal(page.elements.status.textContent, "");
});

test("an unpaired phone sees the heading once open_board says nothing is saved", async () => {
  const page = load({
    intake_intent: async () => null,
    open_board: async () => null,
  });
  await settle();
  assert.equal(page.elements.intro.hidden, false);
  assert.equal(page.elements.status.textContent, "");
});

test("a retryable intake failure goes to the unreachable section and retries", async () => {
  const page = load({
    intake_intent: () => unreachable(),
    open_board: () => unreachable(),
  });
  await settle();
  assert.equal(page.elements.unreachable.hidden, false);
  assert.equal(page.elements.paste.hidden, true);
  assert.equal(page.elements.intro.hidden, true);
  assert.equal(page.timers.filter((t) => t.live).length, 1);
});

test("a final intake failure shows the form and the message", async () => {
  const page = load({
    intake_intent: () => Promise.reject({ message: "Nope.", retry: false }),
    open_board: async () => null,
  });
  await settle();
  assert.equal(page.elements.paste.hidden, false);
  assert.equal(page.elements.error.textContent, "Nope.");
});

test("a failure that arrives while hidden schedules nothing", async () => {
  let page;
  page = load({
    intake_intent: async () => null,
    open_board: () => {
      page.document.visibilityState = "hidden";
      return unreachable();
    },
  });
  await settle();
  assert.equal(page.elements.unreachable.hidden, false);
  assert.equal(page.timers.length, 0);
});

test("returning to the foreground does nothing when the unreachable section is not shown", async () => {
  const page = load({
    intake_intent: async () => null,
    open_board: async () => null,
  });
  await settle();
  page.documentListeners.visibilitychange();
  await settle();
  assert.equal(page.invoked.filter((c) => c === "open_board").length, 1);
});

test("Retry is disabled during a dial and overlapping dials are refused", async () => {
  const calls = [];
  const page = load({
    intake_intent: async () => null,
    open_board: () => {
      if (calls.length === 0) {
        calls.push(null);
        return unreachable();
      }
      const dial = pending();
      calls.push(dial);
      return dial.promise;
    },
  });
  await settle();
  page.elements.retry.listeners.click();
  assert.equal(page.elements.retry.disabled, true);
  page.elements.retry.listeners.click();
  page.documentListeners.visibilitychange();
  await settle();
  assert.equal(page.invoked.filter((c) => c === "open_board").length, 2);
  calls[1].reject({ message: "Can't reach 4d92…6ed6.", retry: true });
  await settle();
  assert.equal(page.elements.retry.disabled, false);
});

test("a dial clears the pending retry timer", async () => {
  let first = true;
  const page = load({
    intake_intent: async () => null,
    open_board: () => {
      if (first) {
        first = false;
        return unreachable();
      }
      return pending().promise;
    },
  });
  await settle();
  assert.equal(page.timers.filter((t) => t.live).length, 1);
  page.elements.retry.listeners.click();
  assert.equal(page.timers.filter((t) => t.live).length, 0);
});

test("the foreground retry starts again at one second", async () => {
  const page = load({
    intake_intent: async () => null,
    open_board: () => unreachable(),
  });
  await settle();
  for (let i = 0; i < 3; i++) {
    const next = page.timers.filter((t) => t.live).at(-1);
    next.live = false;
    next.fn();
    await settle();
  }
  page.document.visibilityState = "hidden";
  page.documentListeners.visibilitychange();
  page.document.visibilityState = "visible";
  page.documentListeners.visibilitychange();
  await settle();
  assert.equal(page.timers.filter((t) => t.live).at(-1).ms, 1000);
});

test("Cancel after Pair again returns to the unreachable section for a saved laptop", async () => {
  const page = load({
    intake_intent: async () => null,
    open_board: () => unreachable(),
    cancel_pairing: async () => {},
  });
  await settle();
  page.elements.again.listeners.click();
  assert.equal(page.elements.paste.hidden, false);
  page.elements.no.listeners.click();
  await settle();
  assert.equal(page.elements.unreachable.hidden, false);
  assert.equal(page.elements.paste.hidden, true);
  assert.equal(page.elements["unreachable-message"].textContent, "Can't reach 4d92…6ed6.");
});

test("Cancel for an unpaired phone shows the form", async () => {
  const page = load({
    intake_intent: async () => null,
    open_board: async () => null,
    cancel_pairing: async () => {},
  });
  await settle();
  page.elements.no.listeners.click();
  await settle();
  assert.equal(page.elements.paste.hidden, false);
  assert.equal(page.elements.unreachable.hidden, true);
});

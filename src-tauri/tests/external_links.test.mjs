import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";

const script = readFileSync(
  new URL("../src/external_links.js", import.meta.url),
  "utf8",
);
const origin = "http://localhost:49152";

function load() {
  const calls = { assigned: [], opened: [], invoked: [] };
  const native = { native: true };
  class HTMLAnchorElement {
    constructor(href, target = "") {
      this.href = href;
      this.target = target;
    }
  }
  const listeners = [];
  const window = {
    location: {
      href: `${origin}/f0123456789ab/`,
      origin,
      assign(url) {
        calls.assigned.push(url);
      },
    },
    open(...args) {
      calls.opened.push(args);
      return native;
    },
    addEventListener(type, listener, capture) {
      listeners.push({ type, listener, capture });
    },
    __TAURI_INTERNALS__: {
      invoke(command, args) {
        calls.invoked.push({ command, ...args });
        return Promise.resolve();
      },
    },
  };
  vm.runInNewContext(script, { window, URL, HTMLAnchorElement });
  const listener = listeners.find(
    (entry) => entry.type === "click" && entry.capture,
  ).listener;
  return {
    calls,
    native,
    window,
    anchor: (href, target) => new HTMLAnchorElement(href, target),
    click(anchor, modifiers = {}) {
      const event = {
        defaultPrevented: false,
        button: 0,
        metaKey: false,
        altKey: false,
        ctrlKey: false,
        shiftKey: false,
        ...modifiers,
        composedPath: () => [anchor],
        preventDefault() {
          this.defaultPrevented = true;
        },
      };
      listener(event);
      return event;
    },
  };
}

test("a same-origin blank link stays in the web view", () => {
  const page = load();
  const event = page.click(page.anchor(`${origin}/f0123456789ab/task/3`, "_blank"));
  assert.equal(event.defaultPrevented, true);
  assert.deepEqual(page.calls.assigned, [`${origin}/f0123456789ab/task/3`]);
  assert.deepEqual(page.calls.invoked, []);
  assert.deepEqual(page.calls.opened, []);
});

test("a same-origin modified click stays in the web view", () => {
  const page = load();
  const event = page.click(
    page.anchor(`${origin}/f0123456789ab/task/3`, ""),
    { ctrlKey: true },
  );
  assert.equal(event.defaultPrevented, true);
  assert.deepEqual(page.calls.assigned, [`${origin}/f0123456789ab/task/3`]);
  assert.deepEqual(page.calls.invoked, []);
});

test("a link to another origin opens in the system browser", () => {
  const page = load();
  const event = page.click(page.anchor("https://github.com/atqamz/hand", "_blank"));
  assert.equal(event.defaultPrevented, true);
  assert.deepEqual(page.calls.invoked, [
    { command: "plugin:opener|open_url", url: "https://github.com/atqamz/hand" },
  ]);
  assert.deepEqual(page.calls.assigned, []);
});

test("a mailto link opens in the system browser", () => {
  const page = load();
  const event = page.click(
    page.anchor("mailto:board@example.test", "_blank"),
  );
  assert.equal(event.defaultPrevented, true);
  assert.deepEqual(page.calls.invoked, [
    { command: "plugin:opener|open_url", url: "mailto:board@example.test" },
  ]);
  assert.deepEqual(page.calls.assigned, []);
});

test("a plain same-origin link is left to the web view", () => {
  const page = load();
  const event = page.click(page.anchor(`${origin}/f0123456789ab/task/3`, ""));
  assert.equal(event.defaultPrevented, false);
  assert.deepEqual(page.calls, { assigned: [], opened: [], invoked: [] });
});

test("window.open on a same-origin address navigates the web view", () => {
  const page = load();
  assert.equal(page.window.open(`${origin}/f0123456789ab/task/3`), null);
  assert.deepEqual(page.calls.assigned, [`${origin}/f0123456789ab/task/3`]);
  assert.deepEqual(page.calls.invoked, []);
});

test("window.open on another origin opens in the system browser", () => {
  const page = load();
  assert.equal(page.window.open("https://github.com/atqamz/hand"), null);
  assert.deepEqual(page.calls.invoked, [
    { command: "plugin:opener|open_url", url: "https://github.com/atqamz/hand" },
  ]);
  assert.deepEqual(page.calls.assigned, []);
});

test("window.open on another scheme is left to the web view", () => {
  const page = load();
  assert.equal(page.window.open("about:blank"), page.native);
  assert.deepEqual(page.calls.opened, [["about:blank"]]);
  assert.deepEqual(page.calls.assigned, []);
  assert.deepEqual(page.calls.invoked, []);
});

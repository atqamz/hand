(() => {
  const open = window.open;
  const external = ["http:", "https:", "mailto:", "tel:"];

  const inside = (target) =>
    (target.protocol === "http:" || target.protocol === "https:") &&
    target.origin === window.location.origin;

  const send = (target) => {
    if (inside(target)) {
      window.location.assign(target.href);
      return;
    }
    window.__TAURI_INTERNALS__
      .invoke("plugin:opener|open_url", { url: target.href })
      .catch(() => {});
  };

  window.open = function (url, ...rest) {
    if (url === undefined || url === null || String(url) === "") {
      return open.call(window, url, ...rest);
    }
    let target;
    try {
      target = new URL(String(url), window.location.href);
    } catch {
      return open.call(window, url, ...rest);
    }
    if (!external.includes(target.protocol)) {
      return open.call(window, url, ...rest);
    }
    send(target);
    return null;
  };

  window.addEventListener(
    "click",
    (event) => {
      if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.altKey) {
        return;
      }
      const anchor = event
        .composedPath()
        .find((node) => node instanceof HTMLAnchorElement);
      if (!anchor || !anchor.href) {
        return;
      }
      if (anchor.target.toLowerCase() !== "_blank" && !event.ctrlKey && !event.shiftKey) {
        return;
      }
      let target;
      try {
        target = new URL(anchor.href);
      } catch {
        return;
      }
      if (!external.includes(target.protocol)) {
        return;
      }
      event.preventDefault();
      send(target);
    },
    true,
  );
})();

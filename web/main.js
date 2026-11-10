const invoke = window.__TAURI__.core.invoke;
const intro = document.getElementById("intro");
const paste = document.getElementById("paste");
const input = document.getElementById("link");
const scan = document.getElementById("scan");
const confirm = document.getElementById("confirm");
const laptop = document.getElementById("laptop");
const yes = document.getElementById("yes");
const no = document.getElementById("no");
const unreachable = document.getElementById("unreachable");
const unreachableMessage = document.getElementById("unreachable-message");
const retry = document.getElementById("retry");
const again = document.getElementById("again");
const status = document.getElementById("status");
const error = document.getElementById("error");

let timer = null;
let backoff = 1000;
let dialing = false;
let saved = null;

function message(failure) {
  if (typeof failure === "string") return failure;
  if (failure && failure.message) return failure.message;
  return String(failure);
}

function clearTimer() {
  if (timer !== null) {
    clearTimeout(timer);
    timer = null;
  }
}

function reset() {
  clearTimer();
  backoff = 1000;
  status.textContent = "";
  error.textContent = "";
  confirm.hidden = true;
  unreachable.hidden = true;
  intro.hidden = false;
  paste.hidden = false;
  laptop.textContent = "";
  yes.disabled = false;
  no.disabled = false;
}

function offer(view) {
  clearTimer();
  backoff = 1000;
  error.textContent = "";
  status.textContent = view.notice || "";
  laptop.textContent = view.laptop;
  intro.hidden = true;
  paste.hidden = true;
  unreachable.hidden = true;
  confirm.hidden = false;
  yes.disabled = false;
  no.disabled = false;
}

function failed(failure) {
  error.textContent = message(failure);
  status.textContent = "";
  yes.disabled = false;
  no.disabled = false;
}

async function offerText(text) {
  clearTimer();
  error.textContent = "";
  status.textContent = "Checking the link…";
  try {
    offer(await invoke("offer_link", { text }));
  } catch (failure) {
    status.textContent = "";
    failed(failure);
  }
}

paste.addEventListener("submit", (event) => {
  event.preventDefault();
  const text = input.value;
  input.value = "";
  offerText(text);
});

scan.addEventListener("click", async () => {
  scan.disabled = true;
  clearTimer();
  error.textContent = "";
  status.textContent = "Starting the camera…";
  try {
    const text = await invoke("scan_code");
    if (!text) {
      status.textContent = "";
      return;
    }
    input.value = text;
    paste.requestSubmit();
  } catch (failure) {
    status.textContent = "";
    failed(failure);
  } finally {
    scan.disabled = false;
  }
});

yes.addEventListener("click", pair);
no.addEventListener("click", async () => {
  reset();
  try {
    await invoke("cancel_pairing");
  } catch (failure) {
    failed(failure);
    return;
  }
  if (saved) unreach(saved);
});

async function pair() {
  clearTimer();
  error.textContent = "";
  status.textContent = "Connecting…";
  yes.disabled = true;
  no.disabled = true;
  try {
    await invoke("confirm_pairing");
  } catch (failure) {
    failed(failure);
    if (failure && failure.retry) {
      status.textContent = "";
      timer = setTimeout(pair, backoff);
      backoff = Math.min(backoff * 2, 30000);
    }
  }
}

function foreground() {
  return document.visibilityState !== "hidden";
}

function unreach(failure) {
  clearTimer();
  saved = failure;
  intro.hidden = true;
  paste.hidden = true;
  confirm.hidden = true;
  status.textContent = "";
  error.textContent = "";
  unreachable.hidden = false;
  unreachableMessage.textContent = message(failure);
  retry.disabled = false;
  if (failure && failure.retry && foreground()) {
    timer = setTimeout(openBoard, backoff);
    backoff = Math.min(backoff * 2, 30000);
  }
}

async function openBoard() {
  clearTimer();
  if (dialing) return;
  dialing = true;
  retry.disabled = true;
  error.textContent = "";
  status.textContent = "Connecting…";
  try {
    const board = await invoke("open_board");
    if (board) {
      window.location.href = board;
      return;
    }
    saved = null;
    reset();
  } catch (failure) {
    unreach(failure);
  } finally {
    dialing = false;
  }
}

retry.addEventListener("click", () => {
  backoff = 1000;
  openBoard();
});
again.addEventListener("click", reset);
document.addEventListener("visibilitychange", () => {
  if (unreachable.hidden) return;
  if (foreground()) {
    backoff = 1000;
    openBoard();
  } else {
    clearTimer();
  }
});

async function intake() {
  try {
    const view = await invoke("intake_intent");
    if (view && view.url) {
      window.location.href = view.url;
      return;
    }
    if (view) {
      offer(view);
      return;
    }
  } catch (failure) {
    if (failure && failure.retry) {
      unreach(failure);
      return;
    }
    reset();
    failed(failure);
    return;
  }
  openBoard();
}

const query = new URLSearchParams(window.location.search);
if (query.get("forget") === "1") {
  history.replaceState(null, "", window.location.pathname);
  invoke("forget_laptop").catch(failed);
}

intake();

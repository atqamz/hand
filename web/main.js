const invoke = window.__TAURI__.core.invoke;
const paste = document.getElementById("paste");
const input = document.getElementById("link");
const confirm = document.getElementById("confirm");
const laptop = document.getElementById("laptop");
const yes = document.getElementById("yes");
const no = document.getElementById("no");
const status = document.getElementById("status");
const error = document.getElementById("error");

let timer = null;
let backoff = 1000;

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
  paste.hidden = true;
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

paste.addEventListener("submit", async (event) => {
  event.preventDefault();
  clearTimer();
  error.textContent = "";
  status.textContent = "Checking the link…";
  const text = input.value;
  input.value = "";
  try {
    offer(await invoke("offer_link", { text }));
  } catch (failure) {
    status.textContent = "";
    failed(failure);
  }
});

yes.addEventListener("click", pair);
no.addEventListener("click", async () => {
  reset();
  try {
    await invoke("cancel_pairing");
  } catch (failure) {
    failed(failure);
  }
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

async function intake() {
  try {
    const view = await invoke("intake_intent");
    if (view) {
      offer(view);
      return;
    }
    const board = await invoke("open_board");
    if (board) window.location.href = board;
  } catch (failure) {
    failed(failure);
  }
}

const query = new URLSearchParams(window.location.search);
if (query.get("forget") === "1") {
  history.replaceState(null, "", window.location.pathname);
  invoke("forget_laptop").catch(failed);
}

intake();

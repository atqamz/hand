const invoke = window.__TAURI__.core.invoke;
const proxy = document.getElementById("proxy");
const phone = document.getElementById("phone");
const status = document.getElementById("status");
const core = document.getElementById("core");
const form = document.getElementById("open");

invoke("proxy_url")
  .then((url) => {
    proxy.textContent = url;
  })
  .catch((error) => {
    proxy.textContent = String(error);
  });

invoke("phone_endpoint_id")
  .then((id) => {
    phone.textContent = id;
  })
  .catch((error) => {
    phone.textContent = String(error);
  });

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  core.textContent = "";
  status.textContent = "Connecting…";
  const fleet = document.getElementById("fleet").value.trim();
  try {
    const url = await invoke("open_board", {
      endpointId: document.getElementById("endpoint").value.trim(),
      relay: document.getElementById("relay").value.trim() || null,
      fleet: fleet || null,
    });
    window.location.replace(url);
  } catch (error) {
    status.textContent = "";
    core.textContent = String(error);
  }
});

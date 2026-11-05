const core = document.getElementById("core");
window.__TAURI__.core
  .invoke("about")
  .then((text) => {
    core.textContent = text;
  })
  .catch((error) => {
    core.textContent = String(error);
  });

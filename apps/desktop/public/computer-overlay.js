/* This isolated surface has no chat, files, shell, or general IPC API. */
const control = window.computerControl;
let revision = 0;
function render(state) {
  document.body.dataset.mode = state.mode;
  document.getElementById("status").textContent = state.status;
  document.getElementById("target").textContent = state.target;
  for (const action of ["pause", "resume", "stop"]) document.getElementById(action).textContent = state[action];
  document.getElementById("pause").hidden = state.mode !== "ready";
  document.getElementById("resume").hidden = state.mode === "ready";
}
control.onStatus(state => { revision++; render(state); });
const initial = revision;
control.status().then(state => { if (revision === initial) render(state); }).catch(() => {});
for (const action of ["pause", "resume", "stop"]) document.getElementById(action).addEventListener("click", () => {
  control.control(action).catch(() => {});
});

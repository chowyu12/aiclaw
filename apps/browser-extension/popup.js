const LABELS = {
  connected: "已连上 AIClaw",
  connecting: "正在连接…",
  offline: "没连上",
  rejected: "配对码不对",
  unpaired: "还没配对",
};

async function render() {
  const { status = "offline", detail = "" } = await chrome.storage.session.get(["status", "detail"]);
  document.getElementById("status").textContent = LABELS[status] ?? status;
  document.getElementById("detail").textContent = detail;
  document.getElementById("dot").classList.toggle("ok", status === "connected");
  const { pairToken = "" } = await chrome.storage.local.get("pairToken");
  document.getElementById("token").placeholder = pairToken ? "已保存（重新粘贴可替换）" : "粘贴配对码";
}

document.getElementById("save").addEventListener("click", async () => {
  const value = document.getElementById("token").value.trim();
  if (!value) return;
  await chrome.storage.local.set({ pairToken: value });
  document.getElementById("token").value = "";
  setTimeout(render, 800);
});

chrome.storage.onChanged.addListener(() => void render());
void render();

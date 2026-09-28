const code = document.getElementById("code");
const result = document.getElementById("result");
const allow = document.getElementById("allow");
const reject = document.getElementById("reject");

async function render() {
  const { pairCode } = await chrome.storage.session.get("pairCode");
  if (typeof pairCode === "string" && pairCode) {
    code.textContent = pairCode;
    allow.disabled = false;
  } else if (!result.textContent) {
    code.textContent = "····";
    allow.disabled = true;
    result.className = "result bad";
    result.textContent = "配对请求已经失效。AIClaw 会重新发起，这个页面会自动刷新。";
  }
}

async function decide(yes) {
  allow.disabled = true;
  reject.disabled = true;
  const reply = await chrome.runtime.sendMessage({ type: "pairDecision", allow: yes }).catch((error) => ({ ok: false, error: String(error) }));
  if (!reply?.ok) {
    result.className = "result bad";
    result.textContent = reply?.error ?? "没能送达，稍后再试。";
    allow.disabled = false;
    reject.disabled = false;
    return;
  }
  result.className = yes ? "result ok" : "result";
  result.textContent = yes ? "已允许，正在连接 AIClaw…这个页面会自动关掉。" : "已拒绝。之后要连接，点工具栏上 AIClaw 图标里的「重新配对」。";
}

allow.addEventListener("click", () => void decide(true));
reject.addEventListener("click", () => void decide(false));
chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "session" && changes.pairCode) void render();
  if (area === "session" && changes.status?.newValue === "connected") {
    result.className = "result ok";
    result.textContent = "已连上 AIClaw。";
  }
});
void render();

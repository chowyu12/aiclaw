/** Real Chromium regression for workflows, with a local fixture and no model calls. */
const { app, BrowserWindow } = require("electron");
const { strict: assert } = require("node:assert");
const { createServer } = require("node:http");
const { mkdtempSync } = require("node:fs");
const { join } = require("node:path");
const { tmpdir } = require("node:os");

app.setPath("userData", mkdtempSync(join(tmpdir(), "aiclaw-browser-workflow-")));
app.on("window-all-closed", () => {});
const windowScript = () => BrowserWindow.getAllWindows()[0].webContents;
const fixture = `<!doctype html>
<input aria-label="Query"><select aria-label="Category"><option>Old</option><option>New</option></select>
<button>Search</button><p id="result" hidden></p>
<a href="/destination">Destination</a>
<script>
  const [input, select, submit] = document.querySelectorAll("input, select, button");
  window.actions = 0;
  window.resultReads = 0;
  const resultNode = document.querySelector("#result");
  const innerText = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "innerText").get;
  Object.defineProperty(resultNode, "innerText", {
    get() { window.resultReads++; return innerText.call(this); },
  });
  input.oninput = () => {
    select.disabled = true;
    setTimeout(() => { select.disabled = false; }, 130);
    if (input.value === "replace") select.replaceWith(select.cloneNode(true));
    if (input.value === "navigate") history.pushState({}, "", "/other");
  };
  submit.onclick = () => {
    window.actions++;
    setTimeout(() => {
      const result = document.querySelector("#result");
      result.textContent = input.value + " / " + select.value;
      result.hidden = false;
    }, 130);
  };
</script>`;
const server = createServer((request, response) => response.writeHead(200, { "Content-Type": "text/html" }).end(
  request.url === "/auto-submit"
    ? '<form method="post" action="/auto-result"><select aria-label="Author" onchange="this.form.submit()"><option>Old</option><option>New</option></select></form>'
    : fixture,
));
const watchdog = setTimeout(() => app.exit(1), 30_000);

app.whenReady().then(async () => {
  const { AgentBrowser, TurnBrowsers } = await import("../apps/desktop/dist/main/browser.js");
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const url = `http://127.0.0.1:${server.address().port}`;
  const browser = new AgentBrowser();
  const navigate = (path = "/") => browser.perform({ action: "navigate", url: url + path });
  const steps = [
    { action: "type", index: 1, text: "hello" },
    { action: "select", index: 2, value: "New" },
    { action: "click", index: 3 },
  ];
  try {
    await navigate();
    const result = await browser.perform({
      action: "batch", steps, condition: { kind: "text_contains", selector: "#result", value: "hello / New" },
    });
    assert.equal(Boolean(result.failed), false, result.text);
    assert.match(result.text, /Completed 3\/3 actions\. Verification matched/);
    assert.match(result.text, /Use this result directly/);
    assert.match(result.text, /Matched evidence:\n\[Web content begins[^\n]*\]\nhello \/ New\n\[Web content ends\]/);
    assert.equal(await windowScript().executeJavaScript("window.actions"), 1);
    assert.equal(await windowScript().executeJavaScript("window.resultReads"), 1);

    assert.equal((await browser.perform({ action: "wait", condition: { kind: "visible", selector: "#result" } })).failed, false);
    assert.equal((await browser.perform({ action: "verify", condition: { kind: "enabled", selector: "button" } })).failed, false);
    assert.equal((await browser.perform({ action: "verify", condition: { kind: "url_contains", value: "127.0.0.1" } })).failed, false);
    const missing = await browser.perform({ action: "verify", condition: { kind: "text_contains", selector: "#result", value: "missing", timeout_ms: 120 } });
    assert.equal(missing.failed, true);
    assert.match(missing.text, /timed out/);
    assert.doesNotMatch(missing.text, /Matched evidence:/);

    await navigate("/delayed-observation");
    await windowScript().executeJavaScript(`setTimeout(() => {
      const result = document.querySelector("#result");
      result.textContent = "Ready later";
      result.hidden = false;
    }, 1700); void 0`);
    const delayed = { kind: "text_contains", selector: "#result", value: "Ready later" };
    assert.equal((await browser.perform({ action: "wait", condition: delayed })).failed, true);
    assert.equal((await browser.perform({ action: "wait", condition: { ...delayed, timeout_ms: 3000 } })).failed, false);

    await navigate("/failure");
    await assert.rejects(browser.perform({ action: "batch", steps, condition: { kind: "visible", selector: "[" } }), /Invalid CSS selector/);
    assert.equal(await windowScript().executeJavaScript("document.querySelector('input').value"), "");
    for (const text of ["replace", "navigate"]) {
      await navigate("/" + text + "-fixture");
      const stale = await browser.perform({ action: "batch", steps: [{ ...steps[0], text }, ...steps.slice(1)] });
      assert.equal(stale.failed, true);
      assert.match(stale.text, /after 1\/3 completed actions/);
      assert.equal(await windowScript().executeJavaScript("window.actions"), 0);
    }

    await navigate("/failed-proof");
    const failedProof = await browser.perform({ action: "batch", steps, condition: { kind: "text_contains", selector: "#result", value: "wrong", timeout_ms: 250 } });
    assert.equal(failedProof.failed, true);
    assert.match(failedProof.text, /Completed 3\/3 actions; verification failed\. Do not replay/);
    assert.equal(await windowScript().executeJavaScript("window.actions"), 1);

    await navigate("/links");
    const link = { action: "click", index: 4 };
    const blocked = await browser.perform({ action: "batch", steps: [link, steps[2]] });
    assert.equal(blocked.failed, true);
    assert.match(blocked.text, /after 0\/2 completed actions/);
    const destination = await browser.perform({ action: "batch", steps: [link], condition: { kind: "url_contains", value: "/destination" } });
    assert.equal(destination.failed, false, destination.text);
    assert.match(destination.text, /Matched evidence:\n[^\n]*\nhttp:\/\/127\.0\.0\.1:\d+\/destination/);
    const clicked = await browser.perform({ action: "click", index: 4 });
    assert.match(clicked.text, /Visible page text \(first 1200 characters\):/);

    await navigate("/auto-submit");
    const selected = await browser.perform({ action: "select", index: 1, value: "New" });
    assert.match(selected.text, /\/auto-result/);
    assert.match(selected.text, /\[2\] combobox "Category"/);
  } finally {
    await browser.close();
  }

  const turns = new TurnBrowsers(() => "builtin");
  const turn = { sessionId: "fixture", turnId: "cancel" };
  turns.start(turn.sessionId, turn.turnId);
  await turns.perform({ ...turn, action: "navigate", url });
  const waiting = turns.perform({ ...turn, action: "wait", condition: { kind: "visible", selector: "#result" } });
  const queued = turns.perform({ ...turn, action: "batch", steps });
  const pending = Promise.allSettled([waiting, queued]);
  await turns.finish(turn.sessionId, turn.turnId);
  assert.deepEqual((await pending).map(result => result.status), ["rejected", "rejected"]);
  assert.equal(BrowserWindow.getAllWindows().length, 0);
  console.log("Browser workflows verified in Chromium: batching, readiness, verification, stale targets, navigation, and turn cancellation.");
}).then(() => {
  clearTimeout(watchdog);
  server.close();
  app.exit(0);
}).catch(error => {
  console.error(error);
  clearTimeout(watchdog);
  server.close();
  app.exit(1);
});

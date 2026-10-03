import test from "node:test";
import assert from "node:assert/strict";
import { once } from "node:events";
import { AgentTransport } from "../packages/agent-client/src/transport.ts";

const helper = `
const readline = require('node:readline');
readline.createInterface({input:process.stdin}).on('line', line => {
 const f = JSON.parse(line);
 if (f.method === 'crash') process.exit(2);
 if (f.method === 'shutdown') process.exit(0);
 if (f.method === 'echo') console.log(JSON.stringify({id:f.id,result:f.params}));
});
process.stdin.on('end', () => process.exit(3));
`;

test("transport sends shutdown before closing stdin and can restart", { timeout: 8000 }, async () => {
 const transport = new AgentTransport({ command: process.execPath, args: ["-e", helper] });
 transport.start();
 assert.deepEqual(await transport.request("echo", { x: 1 }), { x: 1 });
 const exited = once(transport, "exit");
 await transport.stop();
 assert.equal((await exited)[0], 0);
 transport.start();
 assert.deepEqual(await transport.request("echo", { x: 2 }), { x: 2 });
 await transport.stop();
});

test("process loss rejects pending input and subsequent requests without replay", { timeout: 5000 }, async () => {
 const transport = new AgentTransport({ command: process.execPath, args: ["-e", helper] });
 transport.start();
 await assert.rejects(transport.request("crash"), /submission may have been accepted/);
 assert.equal(transport.running, false);
 await assert.rejects(transport.request("echo"), /not started/);
 await transport.stop();
});

test("spawn failure rejects requests and clears running state", { timeout: 5000 }, async () => {
 const transport = new AgentTransport({ command: "/does-not-exist/aiclaw" });
 transport.start();
 await assert.rejects(transport.request("echo"));
 assert.equal(transport.running, false);
 await transport.stop();
});

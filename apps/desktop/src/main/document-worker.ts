import { parseDocumentAttachment } from "./document-parser.js";

// Electron utility-process MessagePort; Node IPC is used by regression tests.
const port = (process as unknown as { parentPort?: { on(event: string, callback: (event: {data: unknown}) => void): void; postMessage(value: unknown): void } }).parentPort;
async function read(input: unknown): Promise<void> {
  try {
    const value = input as { name: string; data: Uint8Array };
    const result = await parseDocumentAttachment(value.name, new Uint8Array(value.data));
    if (port) port.postMessage({ result }); else process.send?.({ result });
  } catch (error) {
    const reply = { error: error instanceof Error ? error.message : "Could not parse attachment" };
    if (port) port.postMessage(reply); else process.send?.(reply);
  }
}
if (port) port.on("message", event => { void read(event.data); });
else process.on("message", input => { void read(input); });

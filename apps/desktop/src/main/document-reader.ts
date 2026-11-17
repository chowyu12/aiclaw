import { utilityProcess } from "electron";
import { fileURLToPath } from "node:url";
import { ARCHIVE_EXTENSIONS, DOCUMENT_EXTENSIONS, TEXT_EXTENSIONS, attachmentExtension, MAX_DOCUMENT_BYTES, type ParsedAttachment, type AttachmentReadQuery } from "../shared/attachment-files.js";

let queue: Promise<unknown> = Promise.resolve();
let pending = 0;
export async function readDocumentAttachment(input: unknown): Promise<ParsedAttachment> {
  const value = input as { name?: unknown; data?: unknown; query?: AttachmentReadQuery } | null;
  if (!value || typeof value.name !== "string" || value.name.length > 512 || !(value.data instanceof Uint8Array) || !value.data.length || value.data.byteLength > MAX_DOCUMENT_BYTES) throw Error("Invalid attachment or file exceeds 25 MiB");
  const ext = attachmentExtension(value.name);
  if (!DOCUMENT_EXTENSIONS.has(ext) && !ARCHIVE_EXTENSIONS.has(ext) && !TEXT_EXTENSIONS.has(ext)) throw Error("Unsupported attachment format");
  if (pending >= 4) throw Error("Too many pending attachment reads; please retry after the current reads complete");
  pending++;
  const task = queue.then(() => new Promise<ParsedAttachment>((resolve, reject) => {
      const child = utilityProcess.fork(fileURLToPath(new URL("./document-worker.cjs", import.meta.url)), [], {
        serviceName: "AIClaw attachment parser", stdio: "ignore", execArgv: ["--max-old-space-size=256"],
      });
      const finish = (error?: Error, result?: ParsedAttachment) => {
        clearTimeout(timer); child.removeAllListeners(); child.kill();
        if (error) reject(error); else resolve(result!);
      };
      const timer = setTimeout(() => finish(Error("Attachment parsing timed out after 30 seconds")), 30_000);
      child.once("spawn", () => child.postMessage({ name: value.name, data: value.data, query: value.query }));
      child.once("message", (reply: { result?: ParsedAttachment; error?: string }) => {
        if (reply.error || !reply.result) finish(Error(reply.error || "Attachment parsing failed"));
        else finish(undefined, reply.result);
      });
      child.once("exit", () => finish(Error("Attachment parser exited before completing; the file may be damaged or too complex")));
    }));
  queue = task.catch(() => {});
  try { return await task; } finally { pending--; }
}

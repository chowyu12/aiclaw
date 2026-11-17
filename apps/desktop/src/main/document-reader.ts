import { utilityProcess } from "electron";
import { fileURLToPath } from "node:url";
import { ARCHIVE_EXTENSIONS, DOCUMENT_EXTENSIONS, attachmentExtension, MAX_DOCUMENT_BYTES, type ParsedAttachment } from "../shared/attachment-files.js";

let active = false;
export async function readDocumentAttachment(input: unknown): Promise<ParsedAttachment> {
  const value = input as { name?: unknown; data?: unknown } | null;
  if (!value || typeof value.name !== "string" || value.name.length > 512 || !(value.data instanceof Uint8Array) || !value.data.length || value.data.byteLength > MAX_DOCUMENT_BYTES) throw Error("Invalid attachment or file exceeds 25 MiB");
  const ext = attachmentExtension(value.name);
  if (!DOCUMENT_EXTENSIONS.has(ext) && !ARCHIVE_EXTENSIONS.has(ext)) throw Error("Unsupported attachment format");
  if (active) throw Error("Another document is being parsed; please try again when it finishes");
  active = true;
  try {
    return await new Promise<ParsedAttachment>((resolve, reject) => {
      const child = utilityProcess.fork(fileURLToPath(new URL("./document-worker.cjs", import.meta.url)), [], {
        serviceName: "AIClaw attachment parser", stdio: "ignore", execArgv: ["--max-old-space-size=256"],
      });
      const finish = (error?: Error, result?: ParsedAttachment) => {
        clearTimeout(timer); child.removeAllListeners(); child.kill();
        if (error) reject(error); else resolve(result!);
      };
      const timer = setTimeout(() => finish(Error("Attachment parsing timed out after 30 seconds")), 30_000);
      child.once("spawn", () => child.postMessage({ name: value.name, data: value.data }));
      child.once("message", (reply: { result?: ParsedAttachment; error?: string }) => {
        if (reply.error || !reply.result) finish(Error(reply.error || "Attachment parsing failed"));
        else finish(undefined, reply.result);
      });
      child.once("exit", () => finish(Error("Attachment parser exited before completing; the file may be damaged or too complex")));
    });
  } finally { active = false; }
}

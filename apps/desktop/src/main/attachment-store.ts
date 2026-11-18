/** Original uploads are immutable; session membership and parser caches are separate. */
import { createHash, randomBytes, randomUUID } from "node:crypto";
import { constants } from "node:fs";
import { mkdir, lstat, open, readdir, rename, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { ARCHIVE_EXTENSIONS, DOCUMENT_EXTENSIONS, TEXT_EXTENSIONS, attachmentExtension, MAX_DOCUMENT_BYTES, MAX_DOCUMENT_ATTACHMENTS, type AttachmentReadQuery, type AttachmentRef, type ParsedAttachment } from "../shared/attachment-files.js";

interface RecordFile {
  id: string; name: string; size: number; sha256: string; createdAt: number;
  draftSessionId: string; claimHash: string; owners: string[];
}
type Parser = (name: string, data: Uint8Array, query: AttachmentReadQuery) => Promise<ParsedAttachment>;
const digest = (value: Uint8Array | string) => createHash("sha256").update(value).digest("hex");
const validID = (id: string) => /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(id);
const publicRecord = ({ id, name, size, sha256, createdAt }: RecordFile) => ({ id, name, size, sha256, createdAt, originalRetained: true });

export class AttachmentStore {
  private mutations: Promise<unknown> = Promise.resolve();
  private readonly root: string;
  private readonly parse: Parser;
  constructor(root: string, parse: Parser) { this.root = root; this.parse = parse; }
  private async directory(path: string): Promise<void> {
    const info = await lstat(path);
    if (!info.isDirectory() || info.isSymbolicLink() || process.getuid && (info.uid !== process.getuid() || (info.mode & 0o077))) throw Error("Attachment directory must be private, owned and not linked");
  }
  private async initialize(): Promise<void> {
    await mkdir(this.root, { recursive: true, mode: 0o700 });
    await this.directory(this.root);
  }
  private async record(id: string): Promise<RecordFile> {
    if (!validID(id)) throw Error("Invalid attachment ID");
    await this.directory(this.root);
    await this.directory(join(this.root, id));
    const data = await this.readFile(join(this.root, id, "record.json"));
    const record = JSON.parse(data.toString()) as RecordFile;
    if (record.id !== id || !Array.isArray(record.owners) || !Number.isSafeInteger(record.size) || record.size > MAX_DOCUMENT_BYTES || !/^[0-9a-f]{64}$/.test(record.sha256)) throw Error("Attachment metadata is damaged");
    return record;
  }
  private async readFile(path: string): Promise<Buffer> {
    const info = await lstat(path);
    if (!info.isFile() || info.isSymbolicLink()) throw Error("Attachment must be a regular file");
    const file = await open(path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    try {
      if (!(await file.stat()).isFile()) throw Error("Attachment must be a regular file");
      return await file.readFile();
    } finally { await file.close(); }
  }
  private async save(record: RecordFile): Promise<void> {
    const directory = join(this.root, record.id);
    await this.directory(directory);
    const temp = join(directory, `record-${randomUUID()}.tmp`);
    await writeFile(temp, JSON.stringify(record), { mode: 0o600, flag: "wx" });
    await rename(temp, join(directory, "record.json"));
  }
  private serialize<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.mutations.then(operation);
    this.mutations = result.catch(() => {});
    return result;
  }
  async stage(input: unknown): Promise<AttachmentRef> {
    const value = input as { name?: unknown; data?: unknown; sessionId?: unknown } | null;
    if (!value || typeof value.name !== "string" || !value.name.trim() || value.name.length > 512 || !(value.data instanceof Uint8Array) || !value.data.length || value.data.length > MAX_DOCUMENT_BYTES || typeof value.sessionId !== "string") throw Error("Invalid attachment or file exceeds 25 MiB");
    const name = value.name.split(/[\\/]/).pop()!;
    const ext = attachmentExtension(name);
    if (!DOCUMENT_EXTENSIONS.has(ext) && !ARCHIVE_EXTENSIONS.has(ext) && !TEXT_EXTENSIONS.has(ext)) throw Error("Unsupported attachment format");
    await this.initialize();
    const id = randomUUID(), claimToken = randomBytes(24).toString("hex");
    await mkdir(join(this.root, id), { mode: 0o700 });
    await writeFile(join(this.root, id, "original"), value.data, { flag: "wx", mode: 0o600 });
    const record: RecordFile = { id, name, size: value.data.length, sha256: digest(value.data), createdAt: Date.now(), draftSessionId: value.sessionId, claimHash: digest(claimToken), owners: [] };
    await this.save(record);
    return { id, name, size: record.size, sha256: record.sha256, claimToken };
  }
  async bind(sessionId: string, claims: { id: string; claimToken: string }[]): Promise<void> {
    if (!sessionId || !Array.isArray(claims) || claims.length > MAX_DOCUMENT_ATTACHMENTS) throw Error("Invalid attachment session or claims");
    await this.serialize(async () => {
      const records: RecordFile[] = [];
      for (const claim of claims) {
        if (!claim || typeof claim.id !== "string" || typeof claim.claimToken !== "string") throw Error("Invalid attachment claim");
        const record = await this.record(claim.id);
        if (digest(claim.claimToken) !== record.claimHash || !record.owners.includes(sessionId) && (record.owners.length > 0 || record.draftSessionId && record.draftSessionId !== sessionId)) throw Error("Attachment does not belong to this session");
        records.push(record);
      }
      for (const record of records) if (!record.owners.includes(sessionId)) { record.owners.push(sessionId); await this.save(record); }
    });
  }
  async list(sessionId: string) {
    if (!sessionId) throw Error("Attachment reads require a session");
    await this.initialize();
    const result: ReturnType<typeof publicRecord>[] = [];
    for (const id of await readdir(this.root)) {
      if (!validID(id)) continue;
      const record = await this.record(id);
      if (record.owners.includes(sessionId)) result.push(publicRecord(record));
    }
    return result.sort((a, b) => a.createdAt - b.createdAt || a.id.localeCompare(b.id));
  }
  private async ownedOriginal(sessionId: string, id: string) {
    const record = await this.record(id);
    if (!sessionId || !record.owners.includes(sessionId)) throw Error("Attachment does not belong to this session");
    const bytes = await this.readFile(join(this.root, id, "original"));
    if (bytes.length !== record.size || digest(bytes) !== record.sha256) throw Error("Original attachment changed or is damaged; upload it again");
    return { record, bytes };
  }
  async original(sessionId: string, id: string) {
    const { record, bytes } = await this.ownedOriginal(sessionId, id);
    if (!(await this.record(id)).owners.includes(sessionId)) throw Error("Attachment is no longer associated with this session");
    return { name: record.name, bytes };
  }
  async read(sessionId: string, id: string, query: AttachmentReadQuery = { mode: "index" }) {
    const { record, bytes } = await this.ownedOriginal(sessionId, id);
    const cache = join(this.root, id, `parsed-v2-${digest(JSON.stringify(query))}.json`);
    let parsed: ParsedAttachment;
    try { parsed = JSON.parse((await this.readFile(cache)).toString()) as ParsedAttachment; }
    catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
      parsed = await this.parse(record.name, bytes, query);
      const temporary = cache + "." + randomUUID() + ".tmp";
      await writeFile(temporary, JSON.stringify(parsed), { flag: "wx", mode: 0o600 });
      await rename(temporary, cache);
    }
    if (!(await this.record(id)).owners.includes(sessionId)) throw Error("Attachment is no longer associated with this session");
    return { attachment: publicRecord(record), ...parsed };
  }
  async fork(source: string, target: string): Promise<void> {
    await this.serialize(async () => {
      for (const item of await this.list(source)) {
        const record = await this.record(item.id);
        if (!record.owners.includes(target)) { record.owners.push(target); await this.save(record); }
      }
    });
  }
  async removeSession(sessionId: string): Promise<void> {
    await this.serialize(async () => {
      for (const item of await this.list(sessionId)) {
        const record = await this.record(item.id);
        record.owners = record.owners.filter(id => id !== sessionId);
        await this.save(record);
      }
    });
  }
}

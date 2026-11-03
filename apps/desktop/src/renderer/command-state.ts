import { reactive } from "vue";

interface RunState { id: string; sessionId: string; status: "starting" | "running" | "stopping" | "finished"; output: string; code: number | null; stopped: boolean; error: string }
type Event = {id: string; sessionId: string; type: "output" | "exit"; text?: string; code?: number | null; stopped?: boolean; error?: string};
export const commandRuns = reactive<Record<string, RunState>>({});
let subscribed = false;
function subscribe(): void {
  if (subscribed) return;
  window.aiclaw.on.command((value: unknown) => {
    const event = value as Event;
    const run = Object.values(commandRuns).find(item => item.id === event.id && item.sessionId === event.sessionId);
    if (!run) return;
    if (event.type === "output") run.output += event.text ?? "";
    else if (event.type === "exit") { run.status = "finished"; run.code = event.code ?? null; run.stopped = event.stopped === true; run.error = event.error ?? ""; }
  });
  subscribed = true;
}
export async function runCommand(key: string, sessionId: string, command: string): Promise<void> {
  if (commandRuns[key] && commandRuns[key].status !== "finished") return;
  subscribe();
  const finished = Object.keys(commandRuns).filter(item => commandRuns[item]?.status === "finished");
  while (finished.length >= 20) delete commandRuns[finished.shift()!];
  const id = crypto.randomUUID();
  const run: RunState = reactive({id, sessionId, status:"starting", output:"", code:null, stopped:false, error:""});
  commandRuns[key] = run;
  try {
    await window.aiclaw.command.run({id, sessionId, command});
    if (run.status === "starting") run.status = "running";
  } catch (error) { run.status = "finished"; run.error = error instanceof Error ? error.message : String(error); }
}
export async function stopCommand(key: string): Promise<void> {
  const run = commandRuns[key];
  if (!run || run.status !== "running") return;
  run.status = "stopping";
  try {
    if (!await window.aiclaw.command.stop(run.id, run.sessionId) && run.status === "stopping") run.status = "finished";
  } catch (error) { run.error = error instanceof Error ? error.message : String(error); if (run.status === "stopping") run.status = "running"; }
}

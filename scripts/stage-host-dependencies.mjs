import { cp } from "node:fs/promises";
import { join } from "node:path";

export async function stageHostDependencies({ repo, destination }) {
  // Explicit production modules; staging must never depend on the repository resolver.
  for (const name of ["@trycua/cua-driver", "@ubjs/core", "@ubjs/node", "fflate"]) {
    await cp(
      join(repo, "node_modules", name),
      join(destination, "node_modules", name),
      { recursive: true },
    );
  }
}

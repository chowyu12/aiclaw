import { cp, mkdir } from "node:fs/promises";
import { join } from "node:path";

export async function stageHostDependencies({ repo, destination }) {
  // Explicit production modules; staging must never depend on the repository resolver.
  for (const name of ["@trycua/cua-driver", "@ubjs/core", "@ubjs/node", "fflate", "unpdf", "7z-wasm", "readline-sync"]) {
    await cp(
      join(repo, "node_modules", name),
      join(destination, "node_modules", name),
      { recursive: true },
    );
  }
  // Legacy Office and XML parsers are bundled into document-worker.cjs. Their
  // unused OCR/PDF dependency trees must not enter the application package.
  for (const [name, license] of [["@jose.espana/docstream", "LICENSE"], ["@xmldom/xmldom", "LICENSE"]]) {
    const directory = join(destination, "licenses", name);
    await mkdir(directory, { recursive: true });
    await cp(join(repo, "node_modules", name, license), join(directory, license));
  }
}

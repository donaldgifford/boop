// Fails when web/src/api/schema.gen.ts is stale against api/openapi.yaml:
// it regenerates into a temporary file and compares. CI's UI job runs it.
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { $ } from "bun";

const committed = "web/src/api/schema.gen.ts";
const dir = await mkdtemp(join(tmpdir(), "boop-ui-"));
const fresh = join(dir, "schema.gen.ts");
try {
  await $`bunx openapi-typescript ../api/openapi.yaml -o ${fresh}`.quiet();
  const [want, got] = await Promise.all([readFile(fresh, "utf8"), readFile(committed, "utf8")]);
  if (want !== got) {
    console.error(`${committed} is stale: run \`bun run gen:api\` and commit the result`);
    process.exit(1);
  }
  console.log(`${committed} is current`);
} finally {
  await rm(dir, { recursive: true, force: true });
}

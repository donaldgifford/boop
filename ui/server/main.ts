import { createApp } from "./app";
import { ConfigError, loadConfig } from "./config";
import { jsonLogger } from "./log";

function main(): void {
  let config;
  try {
    config = loadConfig(process.env);
  } catch (e) {
    if (e instanceof ConfigError) {
      jsonLogger("error", "invalid configuration", { problems: e.problems });
      process.exit(1);
    }
    throw e;
  }
  Bun.serve({ port: config.port, fetch: createApp({ config }).fetch });
  jsonLogger("info", "boopd-ui listening", { port: config.port });
}

main();

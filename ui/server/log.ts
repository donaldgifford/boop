// Logger writes one JSON line per event, in the shape boopd's slog uses.
export type Logger = (level: "info" | "warn" | "error", msg: string, attrs?: Record<string, unknown>) => void;

export const jsonLogger: Logger = (level, msg, attrs = {}) => {
  const line = JSON.stringify({ time: new Date().toISOString(), level, msg, ...attrs });
  (level === "info" ? console.log : console.error)(line);
};

export const discardLogger: Logger = () => {};

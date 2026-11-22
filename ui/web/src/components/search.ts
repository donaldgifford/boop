import { useNavigate } from "@tanstack/react-router";

// oneOf reads a search value that must be one of a page's options.
export function oneOf<T extends string>(v: unknown, allowed: readonly T[]): T | undefined {
  return typeof v === "string" && (allowed as readonly string[]).includes(v) ? (v as T) : undefined;
}

// text reads a non-empty search string.
export function text(v: unknown): string | undefined {
  return typeof v === "string" && v !== "" ? v : undefined;
}

// flag reads a boolean search value, as the router or a typed URL has it.
export function flag(v: unknown): boolean | undefined {
  if (v === true || v === "true") return true;
  if (v === false || v === "false") return false;
  return undefined;
}

// useSearchPatch merges a patch into the current page's search; an
// undefined value drops the key.
export function useSearchPatch<S extends object>() {
  const navigate = useNavigate();
  return (patch: Partial<S>) => void navigate({ to: ".", search: (prev) => ({ ...prev, ...(patch as Record<string, unknown>) }) });
}

// ALL is a segmented control's "no filter" value.
export const ALL = "all";
export const allOr = (v: string) => (v === ALL ? undefined : v);

import { useQuery } from "@tanstack/react-query";

import { api, unwrap } from "./client";

// UIConfig is the BFF's /ui/config: display values only.
export interface UIConfig {
  issuer: string;
  public_url: string;
  session_ttl_seconds: number;
  // links are the optional external links (DESIGN-0004 § The UI).
  links: { label: string; url: string }[];
}

export function useUIConfig() {
  return useQuery({
    queryKey: ["ui-config"],
    queryFn: async (): Promise<UIConfig> => (await fetch("/ui/config")).json() as Promise<UIConfig>,
    staleTime: Infinity,
  });
}

// useMe is the signed-in caller: name, groups, visible owners and
// whether they may recheck.
export function useMe() {
  return useQuery({ queryKey: ["me"], queryFn: () => unwrap(api.GET("/me")), staleTime: 5 * 60_000 });
}

// index.css is the design system's stylesheet: Tailwind, the tokens, both
// themes and the fonts. The app defines no tokens of its own.
import "./index.css";

import { TimeZoneProvider } from "@donaldgifford/design-system/blocks/time";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { newQueryClient } from "./api/query";
import { router } from "./router";

const root = document.getElementById("root");
if (!root) {
  throw new Error("missing #root");
}

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={newQueryClient()}>
      <TimeZoneProvider storageKey="boop.zone">
        <RouterProvider router={router} />
      </TimeZoneProvider>
    </QueryClientProvider>
  </StrictMode>,
);

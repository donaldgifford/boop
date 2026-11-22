import { CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@donaldgifford/design-system/components/command";
import { IconExternalLink } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { useEffect } from "react";

import { useUIConfig } from "../api/queries";
import { GROUP_LABELS, NAV, navGroups } from "./nav";

// useCommandShortcut opens the palette on ⌘K or Ctrl+K.
export function useCommandShortcut(open: () => void): void {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        open();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);
}

interface CommandPaletteProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

// CommandPalette jumps to a page, or opens an external link.
export function CommandPalette({ open, onOpenChange }: CommandPaletteProps) {
  const navigate = useNavigate();
  const links = useUIConfig().data?.links ?? [];
  const go = (run: () => void) => {
    onOpenChange(false);
    run();
  };
  return (
    <CommandDialog open={open} onOpenChange={onOpenChange} title="Go to" description="Jump to a page">
      <CommandInput placeholder="Go to…" />
      <CommandList>
        <CommandEmpty>Nothing matches.</CommandEmpty>
        {navGroups(NAV).map(([group, items]) => (
          <CommandGroup key={group} heading={GROUP_LABELS[group]}>
            {items.map((n) => (
              <CommandItem key={n.to} value={`${n.label} ${n.to}`} onSelect={() => go(() => void navigate({ to: n.to }))}>
                <n.icon aria-hidden="true" />
                {n.label}
              </CommandItem>
            ))}
          </CommandGroup>
        ))}
        {links.length > 0 ? (
          <CommandGroup heading="Links">
            {links.map((l) => (
              <CommandItem key={l.url} value={`${l.label} ${l.url}`} onSelect={() => go(() => window.open(l.url, "_blank", "noopener,noreferrer"))}>
                <IconExternalLink aria-hidden="true" />
                {l.label}
              </CommandItem>
            ))}
          </CommandGroup>
        ) : null}
      </CommandList>
    </CommandDialog>
  );
}

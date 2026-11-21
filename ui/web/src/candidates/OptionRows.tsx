// Candidate for the design system (INV-0005 finding 8): the rows inside a
// picker popover. The package's Popover is the container.
import { cn } from "@donaldgifford/design-system/lib/utils";
import { IconCheck } from "@tabler/icons-react";
import type { ComponentProps } from "react";

export function OptionHeading({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("px-2 pt-2 pb-1 font-semibold text-label-sm text-muted-foreground uppercase tracking-label", className)} {...props} />;
}

interface OptionItemProps extends ComponentProps<"button"> {
  /** Marks the row in force. */
  current?: boolean | undefined;
  /** A mono figure at the row's end: a count, a key. */
  detail?: string | undefined;
}

export function OptionItem({ className, current = false, detail, children, ...props }: OptionItemProps) {
  return (
    <button
      type="button"
      aria-current={current ? "true" : undefined}
      className={cn("flex w-full items-center gap-2 rounded-xs px-2 py-1.5 text-left text-base/compact outline-hidden hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent", className)}
      {...props}
    >
      <span className="w-3.5 shrink-0 text-primary">{current ? <IconCheck aria-hidden="true" className="size-3.5" /> : null}</span>
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {detail ? <span className="font-mono text-label text-muted-foreground">{detail}</span> : null}
    </button>
  );
}

export function OptionSeparator({ className, ...props }: ComponentProps<"hr">) {
  return <hr className={cn("-mx-1 my-1 border-t", className)} {...props} />;
}

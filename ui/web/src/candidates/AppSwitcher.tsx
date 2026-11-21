// Candidate for the design system (INV-0005 finding 9): the sidebar header
// as a menu of the apps a deployment configures. Each entry is a plain
// link to another origin: no shared session and no calls between apps.
// With no other app configured it renders the plain identity.
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger } from "@donaldgifford/design-system/components/dropdown-menu";
import { SidebarMenuButton, useSidebar } from "@donaldgifford/design-system/components/sidebar";
import { AppMark, ShellSidebarIdentity } from "@donaldgifford/design-system/shell/sidebar";
import { IconExternalLink, type TablerIcon } from "@tabler/icons-react";
import type { ReactElement } from "react";

export interface SwitcherApp {
  id: string;
  name: string;
  subtitle?: string | undefined;
  icon: TablerIcon;
  /** The app's origin. Unused for the current app. */
  url: string;
}

// A menu item tints any icon inside it muted, which washes out a mark's
// icon on its coloured square. The marks set their own icon colour, with
// `!` because the item's rule is the more specific one.
const MARK_ICON = "[&_svg]:size-3.5!";

interface AppSwitcherProps {
  apps: readonly SwitcherApp[];
  /** The id of the app this is. */
  current: string;
  /** What the identity is when there is no menu: the app's home link. */
  home: ReactElement;
}

export function AppSwitcher({ apps, current, home }: AppSwitcherProps) {
  const { isMobile } = useSidebar();
  const self = apps.find((app) => app.id === current);
  if (!self) {
    throw new Error(`AppSwitcher: no app with id ${current}`);
  }
  if (apps.length < 2) {
    return (
      <SidebarMenuButton size="lg" tooltip={self.name} render={home}>
        <AppMark icon={self.icon} />
        <span className="grid flex-1 text-left leading-tight">
          <span className="truncate font-medium text-sidebar-foreground">{self.name}</span>
          {self.subtitle ? <span className="truncate text-muted-foreground text-xs">{self.subtitle}</span> : null}
        </span>
      </SidebarMenuButton>
    );
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<SidebarMenuButton size="lg" />}>
        <span className="sr-only">Apps: </span>
        <ShellSidebarIdentity mark={<AppMark icon={self.icon} />} title={self.name} subtitle={self.subtitle} />
      </DropdownMenuTrigger>
      <DropdownMenuContent side={isMobile ? "bottom" : "right"} align="start" sideOffset={6} className="min-w-62">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Apps</DropdownMenuLabel>
          {apps.map((app) =>
            app.id === current ? (
              <DropdownMenuItem key={app.id} aria-current="true" render={home}>
                <AppMark icon={app.icon} className={`size-6 ${MARK_ICON} [&_svg]:text-primary-foreground!`} />
                <SwitcherNames app={app} />
                <span className="font-mono text-label text-muted-foreground">current</span>
              </DropdownMenuItem>
            ) : (
              <DropdownMenuItem key={app.id} render={<a href={app.url} />}>
                <AppMark icon={app.icon} className={`size-6 border bg-muted ${MARK_ICON} [&_svg]:text-foreground!`} />
                <SwitcherNames app={app} />
                <IconExternalLink aria-hidden="true" className="size-3.5 text-muted-foreground" />
              </DropdownMenuItem>
            ),
          )}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function SwitcherNames({ app }: { app: SwitcherApp }) {
  return (
    <span className="grid flex-1 leading-tight">
      <span className="font-medium">{app.name}</span>
      {app.subtitle ? <span className="text-muted-foreground text-xs">{app.subtitle}</span> : null}
    </span>
  );
}

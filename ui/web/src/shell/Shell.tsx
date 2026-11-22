import { InlineLoading } from "@donaldgifford/design-system/blocks/inline-state";
import { TimeZoneToggle } from "@donaldgifford/design-system/blocks/time";
import { Breadcrumb, BreadcrumbItem, BreadcrumbLink, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator } from "@donaldgifford/design-system/components/breadcrumb";
import { Button } from "@donaldgifford/design-system/components/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@donaldgifford/design-system/components/dropdown-menu";
import { Separator } from "@donaldgifford/design-system/components/separator";
import { SidebarInset, SidebarMenuButton, SidebarMenuItem, SidebarProvider, SidebarTrigger, useSidebar } from "@donaldgifford/design-system/components/sidebar";
import { AppSwitcher } from "@donaldgifford/design-system/shell/app-switcher";
import { AvatarMark, ShellSidebar, ShellSidebarGroup, ShellSidebarIdentity } from "@donaldgifford/design-system/shell/sidebar";
import { initialsOf, Kbd, TopBarLayout } from "@donaldgifford/design-system/shell/top-bar";
import { IconExternalLink, IconSearch } from "@tabler/icons-react";
import { Link, Outlet, useLocation, useMatchRoute } from "@tanstack/react-router";
import { Fragment, useRef, useState } from "react";

import type { Schemas } from "../api/client";
import { useMe, useUIConfig } from "../api/queries";
import { CommandPalette, useCommandShortcut } from "./CommandPalette";
import { APPS } from "./config";
import { NoAccess } from "./errors";
import { crumbsFor, GROUP_LABELS, NAV, navGroups } from "./nav";

// Shell is the frame, composed from the design system's parts: a
// full-height sidebar that collapses to an icon rail, and a top bar over
// the content. It renders once /me has answered (the gate): a caller
// who sees no owner gets NoAccess instead of pages that would all 403.
export function Shell() {
  const me = useMe();
  if (me.isPending) {
    return <InlineLoading className="p-6">Signing in…</InlineLoading>;
  }
  if (me.isError) {
    // A 401 is already on its way to sign-in (unwrap); anything else is
    // the route error view's.
    throw me.error;
  }
  if (!me.data.all_owners && me.data.owners.length === 0) {
    return (
      <main className="p-6">
        <NoAccess name={me.data.name} />
      </main>
    );
  }
  return <Frame me={me.data} />;
}

function Frame({ me }: { me: Schemas["Me"] }) {
  const [open, setOpen] = useState(true);
  const [palette, setPalette] = useState(false);
  useCommandShortcut(() => setPalette(true));
  return (
    <SidebarProvider open={open} onOpenChange={setOpen}>
      <AppSidebar me={me} />
      <SidebarInset className="min-w-0">
        <AppTopBar onSearch={() => setPalette(true)} />
        <main className="px-[18px] py-4">
          <div className="mx-auto max-w-[1400px] space-y-5">
            <Outlet />
          </div>
        </main>
      </SidebarInset>
      <CommandPalette open={palette} onOpenChange={setPalette} />
    </SidebarProvider>
  );
}

function AppSidebar({ me }: { me: Schemas["Me"] }) {
  const matchRoute = useMatchRoute();
  const links = useUIConfig().data?.links ?? [];
  const { isMobile, setOpenMobile } = useSidebar();
  const chosen = () => {
    if (isMobile) setOpenMobile(false);
  };
  return (
    <ShellSidebar label="Primary" header={<AppSwitcher apps={APPS} current="boop" home={<Link to="/" onClick={chosen} />} />} footer={<AccountMenu me={me} />}>
      {navGroups(NAV).map(([group, items]) => (
        <ShellSidebarGroup key={group} label={GROUP_LABELS[group]}>
          {items.map((n) => (
            <SidebarMenuItem key={n.to}>
              <SidebarMenuButton
                isActive={Boolean(matchRoute({ to: n.to, fuzzy: n.to !== "/" }))}
                tooltip={n.label}
                render={<Link to={n.to} activeOptions={{ exact: n.to === "/" }} onClick={chosen} />}
              >
                <n.icon aria-hidden="true" />
                <span>{n.label}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </ShellSidebarGroup>
      ))}
      {links.length > 0 ? (
        <ShellSidebarGroup label="Links">
          {links.map((l) => (
            <SidebarMenuItem key={l.url}>
              <SidebarMenuButton tooltip={l.label} render={<a href={l.url} target="_blank" rel="noopener noreferrer" />}>
                <IconExternalLink aria-hidden="true" />
                <span>{l.label}</span>
                <span className="sr-only"> (opens in a new tab)</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </ShellSidebarGroup>
      ) : null}
    </ShellSidebar>
  );
}

// AccountMenu is the caller at the sidebar's foot. Sign out is a POST
// form (the BFF checks its Origin), submitted from the menu item.
function AccountMenu({ me }: { me: Schemas["Me"] }) {
  const { isMobile } = useSidebar();
  const signOut = useRef<HTMLFormElement>(null);
  return (
    <>
      <form ref={signOut} method="post" action="/auth/logout" hidden />
      <DropdownMenu>
        <DropdownMenuTrigger render={<SidebarMenuButton size="lg" />}>
          <span className="sr-only">Account menu: </span>
          <ShellSidebarIdentity mark={<AvatarMark initials={initialsOf(me.name, me.subject)} />} title={me.name} subtitle={me.operator ? "operator" : "viewer"} />
        </DropdownMenuTrigger>
        <DropdownMenuContent side={isMobile ? "bottom" : "right"} align="end" sideOffset={6} className="min-w-[220px]">
          <DropdownMenuGroup>
            <DropdownMenuLabel className="font-normal normal-case tracking-normal">
              <div className="text-foreground">{me.name}</div>
              <div className="font-mono text-[11px]">{me.all_owners ? "every owner" : me.owners.join(", ")}</div>
            </DropdownMenuLabel>
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => signOut.current?.requestSubmit()}>Sign out</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </>
  );
}

function AppTopBar({ onSearch }: { onSearch: () => void }) {
  const { pathname } = useLocation();
  const crumbs = crumbsFor(pathname);
  return (
    <TopBarLayout
      start={
        <>
          <SidebarTrigger />
          <Separator orientation="vertical" className="mx-1 h-4" />
          <Breadcrumb>
            <BreadcrumbList>
              {crumbs.map((c, i) => (
                <Fragment key={`${i}-${c.label}`}>
                  {i > 0 ? <BreadcrumbSeparator /> : null}
                  <BreadcrumbItem>{c.to ? <BreadcrumbLink render={<Link to={c.to} />}>{c.label}</BreadcrumbLink> : <BreadcrumbPage>{c.label}</BreadcrumbPage>}</BreadcrumbItem>
                </Fragment>
              ))}
            </BreadcrumbList>
          </Breadcrumb>
        </>
      }
      end={
        <>
          <Button variant="ghost" size="sm" onClick={onSearch}>
            <IconSearch aria-hidden="true" className="size-3.5" />
            <span>Search</span>
            <Kbd>⌘K</Kbd>
          </Button>
          <TimeZoneToggle />
        </>
      }
    />
  );
}

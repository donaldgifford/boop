import { TimeZoneToggle } from "@donaldgifford/design-system/blocks/time";
import { Breadcrumb, BreadcrumbItem, BreadcrumbList, BreadcrumbPage } from "@donaldgifford/design-system/components/breadcrumb";
import { Button } from "@donaldgifford/design-system/components/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@donaldgifford/design-system/components/dropdown-menu";
import { Separator } from "@donaldgifford/design-system/components/separator";
import { SidebarInset, SidebarMenuBadge, SidebarMenuButton, SidebarMenuItem, SidebarProvider, SidebarTrigger, useSidebar } from "@donaldgifford/design-system/components/sidebar";
import { AvatarMark, ShellSidebar, ShellSidebarGroup, ShellSidebarIdentity } from "@donaldgifford/design-system/shell/sidebar";
import { initialsOf, Kbd, TopBarLayout } from "@donaldgifford/design-system/shell/top-bar";
import { IconSearch } from "@tabler/icons-react";
import { Link, Outlet, useLocation, useMatchRoute } from "@tanstack/react-router";
import { useState } from "react";

import { AppSwitcher } from "../candidates/AppSwitcher";
import { configuredApps, ME } from "./config";
import { GROUP_LABELS, labelFor, NAV, navGroups } from "./nav";

// Shell is the frame, composed from the design system's parts the way
// repo-guardian composes it: a full-height sidebar that collapses to an
// icon rail, and a top bar over the content.
export function Shell() {
  const [open, setOpen] = useState(true);
  return (
    <SidebarProvider open={open} onOpenChange={setOpen}>
      <AppSidebar />
      <SidebarInset className="min-w-0">
        <AppTopBar />
        <div className="px-[18px] py-4">
          <div className="mx-auto max-w-[1400px] space-y-5">
            <Outlet />
          </div>
        </div>
      </SidebarInset>
    </SidebarProvider>
  );
}

function AppSidebar() {
  const matchRoute = useMatchRoute();
  const { isMobile, setOpenMobile } = useSidebar();
  const chosen = () => {
    if (isMobile) setOpenMobile(false);
  };
  return (
    <ShellSidebar label="Primary" header={<AppSwitcher apps={configuredApps()} current="boop" home={<Link to="/" onClick={chosen} />} />} footer={<AccountMenu />}>
      {navGroups(NAV).map(([group, items]) => (
        <ShellSidebarGroup key={group} label={GROUP_LABELS[group]}>
          {items.map((n) => {
            const active = Boolean(matchRoute({ to: n.to, fuzzy: n.to !== "/" }));
            return (
              <SidebarMenuItem key={n.to}>
                <SidebarMenuButton isActive={active} tooltip={n.label} render={<Link to={n.to} activeOptions={{ exact: n.to === "/" }} onClick={chosen} />}>
                  <n.icon aria-hidden="true" />
                  <span>{n.label}</span>
                </SidebarMenuButton>
                {n.count === undefined ? null : <SidebarMenuBadge className="font-mono">{n.count.toLocaleString("en-US")}</SidebarMenuBadge>}
              </SidebarMenuItem>
            );
          })}
        </ShellSidebarGroup>
      ))}
    </ShellSidebar>
  );
}

function AccountMenu() {
  const { isMobile } = useSidebar();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<SidebarMenuButton size="lg" />}>
        <span className="sr-only">Account menu: </span>
        <ShellSidebarIdentity mark={<AvatarMark initials={initialsOf(ME.name, ME.subject)} />} title={ME.name} subtitle={ME.subject} />
      </DropdownMenuTrigger>
      <DropdownMenuContent side={isMobile ? "bottom" : "right"} align="end" sideOffset={6} className="min-w-[220px]">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="font-normal normal-case tracking-normal">
            <div className="text-foreground">{ME.name}</div>
            <div className="font-mono text-[11px]">{ME.subject}</div>
          </DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem>Sign out</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function AppTopBar() {
  const { pathname } = useLocation();
  return (
    <TopBarLayout
      start={
        <>
          <SidebarTrigger />
          <Separator orientation="vertical" className="mx-1 h-4" />
          <Breadcrumb>
            <BreadcrumbList>
              <BreadcrumbItem>
                <BreadcrumbPage>{labelFor(pathname)}</BreadcrumbPage>
              </BreadcrumbItem>
            </BreadcrumbList>
          </Breadcrumb>
        </>
      }
      end={
        <>
          <Button variant="ghost" size="sm">
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

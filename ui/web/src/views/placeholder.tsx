import { EmptyState } from "@donaldgifford/design-system/blocks/empty-state";
import { PageHeader } from "@donaldgifford/design-system/blocks/page-header";
import { Button } from "@donaldgifford/design-system/components/button";
import { IconHammer } from "@tabler/icons-react";
import { Link, useLocation } from "@tanstack/react-router";

import { labelFor } from "../shell/nav";

// PlaceholderView stands in for the pages the mockup does not draw yet.
export function PlaceholderView() {
  const { pathname } = useLocation();
  const title = labelFor(pathname);
  return (
    <>
      <PageHeader title={title} />
      <EmptyState
        icon={IconHammer}
        title={`${title} is not in the mockup yet`}
        description="Estate, Pull requests and one repository are drawn so far."
        action={pathname === "/repositories" ? <Button variant="outline" size="sm" nativeButton={false} render={<Link to="/repositories/$id" params={{ id: "812345678" }} />}>Open proxmox-go-sdk</Button> : undefined}
      />
    </>
  );
}

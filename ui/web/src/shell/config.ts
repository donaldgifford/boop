import type { SwitcherApp } from "@donaldgifford/design-system/shell/app-switcher";
import { IconRefresh } from "@tabler/icons-react";

// BOOP is this app in the sidebar header: the package's AppSwitcher with
// one app renders the identity as a link home. Other apps join the list
// when the deployment configures them (INV-0005 finding 9).
export const BOOP: SwitcherApp = { id: "boop", name: "boop", subtitle: "renovate · fleet", icon: IconRefresh, url: "/" };

export const APPS: readonly SwitcherApp[] = [BOOP];

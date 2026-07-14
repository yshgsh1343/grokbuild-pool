import { createHashRouter, Navigate } from "react-router-dom";

import { AppShell } from "@/app/app-shell";
import { AnonymousBoundary, AuthBoundary } from "@/app/auth-boundary";
import { AccountsPage } from "@/features/accounts/accounts-page";
import { LoginPage } from "@/features/auth/login-page";
import { DashboardPage } from "@/features/dashboard/dashboard-page";
import { ImportsPage } from "@/features/imports/imports-page";
import { ProxyPoolPage } from "@/features/proxy-pool/proxy-pool-page";
import { SchedulerPage } from "@/features/scheduler/scheduler-page";
import { JsonSettingsPage } from "@/features/settings/json-page";
import { SettingsPage } from "@/features/settings/settings-page";
import { TokensPage } from "@/features/tokens/tokens-page";

export const router = createHashRouter([
  {
    element: <AnonymousBoundary />,
    children: [{ path: "/login", element: <LoginPage /> }],
  },
  {
    element: <AuthBoundary />,
    children: [
      {
        element: <AppShell />,
        children: [
          { index: true, element: <Navigate to="/dashboard" replace /> },
          { path: "/dashboard", element: <DashboardPage /> },
          { path: "/accounts", element: <AccountsPage /> },
          { path: "/tokens", element: <TokensPage /> },
          { path: "/scheduler", element: <SchedulerPage /> },
          { path: "/proxy-pool", element: <ProxyPoolPage /> },
          { path: "/imports", element: <ImportsPage /> },
          { path: "/settings", element: <SettingsPage /> },
          { path: "/settings/json", element: <JsonSettingsPage /> },
          { path: "/config", element: <Navigate to="/settings" replace /> },
        ],
      },
    ],
  },
  { path: "*", element: <Navigate to="/dashboard" replace /> },
]);

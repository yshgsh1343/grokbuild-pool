import { Navigate, Outlet } from "react-router-dom";

import { useAuth } from "@/shared/auth/auth-context";
import { LoadingState } from "@/shared/components/data-state";

export function AuthBoundary() {
  const { isAuthenticated } = useAuth();
  if (!isAuthenticated) return <Navigate to="/login" replace />;
  return <Outlet />;
}

export function AnonymousBoundary() {
  const { isAuthenticated } = useAuth();
  if (isAuthenticated) return <Navigate to="/dashboard" replace />;
  return <Outlet />;
}

export function BootFallback() {
  return (
    <div className="flex min-h-screen items-center justify-center">
      <LoadingState label="加载中…" />
    </div>
  );
}

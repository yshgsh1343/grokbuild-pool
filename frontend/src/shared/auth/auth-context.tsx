import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { toast } from "sonner";

import { api, configureApi } from "@/shared/api/client";
import type { PoolStats, SettingsSnapshot } from "@/shared/api/types";

type AuthContextValue = {
  adminKey: string | null;
  isAuthenticated: boolean;
  login: (key: string) => Promise<void>;
  logout: () => void;
};

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [adminKey, setAdminKey] = useState<string | null>(null);
  const keyRef = useRef<string | null>(null);

  const logout = useCallback(() => {
    keyRef.current = null;
    setAdminKey(null);
  }, []);

  useEffect(() => {
    configureApi({
      getAdminKey: () => keyRef.current,
      onUnauth: () => {
        keyRef.current = null;
        setAdminKey(null);
        toast.error("鉴权失效，请重新登录");
      },
    });
  }, []);

  const login = useCallback(async (key: string) => {
    const trimmed = key.trim();
    if (!trimmed) throw new Error("请输入 admin_key");

    keyRef.current = trimmed;
    try {
      await api<PoolStats>("/admin/pool/stats");
      await api<SettingsSnapshot>("/admin/settings");
      setAdminKey(trimmed);
    } catch (e) {
      keyRef.current = null;
      setAdminKey(null);
      throw e;
    }
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({
      adminKey,
      isAuthenticated: !!adminKey,
      login,
      logout,
    }),
    [adminKey, login, logout],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}

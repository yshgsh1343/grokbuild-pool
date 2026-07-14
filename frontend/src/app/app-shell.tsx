import {
  Import,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Menu,
  Moon,
  Network,
  Settings,
  Sun,
  Users,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, NavLink, Outlet } from "react-router-dom";

import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { useAuth } from "@/shared/auth/auth-context";
import { useTheme } from "@/shared/hooks/use-theme";
import { cn } from "@/shared/lib/cn";

const navigation = [
  { href: "/dashboard", label: "仪表盘", icon: LayoutDashboard },
  { href: "/accounts", label: "账号", icon: Users },
  { href: "/tokens", label: "Token", icon: KeyRound },
  { href: "/proxy-pool", label: "代理池", icon: Network },
  { href: "/imports", label: "导入任务", icon: Import },
  { href: "/settings", label: "设置", icon: Settings },
] as const;

export function AppShell() {
  const { logout } = useAuth();
  const { theme, toggleTheme } = useTheme();
  const [mobileOpen, setMobileOpen] = useState(false);

  function navLinks(onNavigate?: () => void): ReactNode {
    return navigation.map(({ href, label, icon: Icon }) => (
      <NavLink
        key={href}
        to={href}
        onClick={onNavigate}
        className={({ isActive }) =>
          cn(
            "group flex h-8 items-center gap-2 rounded-md px-2.5 text-xs font-normal text-muted-foreground transition-colors hover:bg-secondary/55 hover:text-foreground",
            isActive && "bg-secondary/60 text-foreground",
          )
        }
      >
        {({ isActive }) => (
          <>
            <span className="flex size-5 shrink-0 items-center justify-center">
              <Icon
                className={cn("size-4 text-muted-foreground", isActive && "text-foreground")}
                strokeWidth={1.8}
              />
            </span>
            {label}
          </>
        )}
      </NavLink>
    ));
  }

  const footer = (
    <div className="flex h-9 items-center gap-1 px-2.5">
      <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">admin</span>
      <Button
        variant="ghost"
        size="icon"
        className="size-7"
        onClick={toggleTheme}
        aria-label="切换主题"
        title={theme === "dark" ? "切换到浅色" : "切换到深色"}
      >
        {theme === "dark" ? <Sun /> : <Moon />}
      </Button>
      <Button
        variant="ghost"
        size="icon"
        className="size-7"
        onClick={logout}
        aria-label="退出"
        title="退出"
      >
        <LogOut />
      </Button>
    </div>
  );

  return (
    <div className="min-h-screen bg-background">
      <aside className="fixed inset-y-0 left-0 z-30 hidden h-screen w-[260px] flex-col overflow-hidden bg-sidebar px-3 py-5 lg:flex">
        <div className="flex h-7 shrink-0 items-center px-2.5">
          <Link to="/dashboard" className="text-base font-semibold tracking-tight">
            grokbuild-pool
          </Link>
        </div>
        <nav className="mt-7 min-h-0 flex-1 space-y-1 overflow-y-auto pr-1" aria-label="主导航">
          {navLinks()}
        </nav>
        <div className="mt-3 shrink-0 border-t border-border pt-3">{footer}</div>
      </aside>

      <div className="flex min-h-screen flex-col lg:pl-[260px]">
        <header className="flex h-12 items-center justify-between border-b border-border px-4 lg:hidden">
          <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
            <SheetTrigger asChild>
              <Button variant="ghost" size="icon" className="size-8" aria-label="打开菜单">
                <Menu className="size-4" />
              </Button>
            </SheetTrigger>
            <SheetContent side="left" className="w-72 bg-sidebar">
              <SheetHeader>
                <SheetTitle>grokbuild-pool</SheetTitle>
                <SheetDescription className="sr-only">导航菜单</SheetDescription>
              </SheetHeader>
              <nav className="mt-4 space-y-1 px-3" aria-label="移动导航">
                {navLinks(() => setMobileOpen(false))}
              </nav>
              <div className="mt-auto border-t border-border p-3">{footer}</div>
            </SheetContent>
          </Sheet>
          <span className="text-sm font-semibold">grokbuild-pool</span>
          <div className="w-8" />
        </header>

        <main className="mx-auto w-full max-w-[1280px] flex-1 px-5 py-8 sm:px-8 lg:py-12">
          <Outlet />
        </main>
        <footer className="px-5 py-4 text-center text-[11px] text-muted-foreground sm:px-8">
          pool · admin
        </footer>
      </div>
    </div>
  );
}

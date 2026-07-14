import type { ReactNode } from "react";

import { cn } from "@/shared/lib/cn";

export function DataTableShell({
  toolbar,
  children,
  footer,
  className,
}: {
  toolbar?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  className?: string;
}) {
  return (
    <section className={cn("flex min-w-0 flex-col gap-3", className)}>
      {toolbar ? (
        <div className="flex min-h-10 flex-wrap items-center justify-between gap-2">{toolbar}</div>
      ) : null}
      <div className="min-w-0 overflow-hidden rounded-lg border border-border bg-card">{children}</div>
      {footer ? <div className="flex min-h-10 items-center">{footer}</div> : null}
    </section>
  );
}

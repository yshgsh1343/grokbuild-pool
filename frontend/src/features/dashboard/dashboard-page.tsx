import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  Flame,
  KeyRound,
  RefreshCw,
  Users,
} from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/shared/api/client";
import type { PoolStats } from "@/shared/api/types";
import { ErrorState } from "@/shared/components/data-state";
import { PageHeader } from "@/shared/components/page-header";
import { formatBytes, formatNumber, formatPercent } from "@/shared/lib/format";
import { cn } from "@/shared/lib/cn";

function getStats() {
  return api<PoolStats>("/admin/pool/stats");
}

export function DashboardPage() {
  const q = useQuery({
    queryKey: ["pool-stats"],
    queryFn: getStats,
    refetchInterval: () =>
      typeof document !== "undefined" && document.visibilityState === "hidden"
        ? false
        : 5000,
  });

  if (q.isError) {
    return (
      <ErrorState
        message={q.error instanceof Error ? q.error.message : "加载失败"}
        onRetry={() => void q.refetch()}
      />
    );
  }

  const s = q.data;
  const loading = q.isPending;
  const avail = s?.accounts_available ?? s?.catalog_active ?? 0;
  const total = s?.accounts_total ?? s?.catalog_count ?? 0;
  const req = s?.requests_total ?? 0;
  const err = s?.errors_total ?? 0;
  const rate = s?.success_rate ?? (req > 0 ? (req - err) / req : 1);
  const hot = s?.pool_hot_size ?? 0;
  const hotCap = s?.hot_cap ?? "—";
  const cool = s?.pool_cooldown_size ?? 0;
  const tokEn = s?.tokens_enabled ?? 0;
  const tokTotal = s?.tokens_total ?? 0;
  const tokEx = s?.tokens_exhausted ?? 0;

  return (
    <div className="space-y-8">
      <PageHeader
        title="仪表盘"
        description={
          s
            ? `更新于 ${new Date().toLocaleString("zh-CN")} · uptime ${Math.round(s.uptime_seconds ?? 0)}s · ${s.listen ?? ""} · ${s.version ?? ""}`
            : "运行态与池容量一览"
        }
        actions={
          <Button
            variant="ghost"
            size="icon"
            onClick={() => void q.refetch()}
            disabled={q.isFetching}
            aria-label="刷新"
          >
            <RefreshCw className={q.isFetching ? "animate-spin" : undefined} />
          </Button>
        }
      />

      <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard
          icon={<Users />}
          label="可用账号"
          value={formatNumber(avail)}
          detail={total ? `${avail} / ${total}` : "冷库 active 且启用"}
          loading={loading}
        />
        <MetricCard
          icon={<Activity />}
          label="请求数"
          value={formatNumber(req)}
          detail={`成功率 ${formatPercent(rate)}${err ? ` · ${err} 失败` : ""}`}
          loading={loading}
        />
        <MetricCard
          icon={<Flame />}
          label="热池"
          value={`${hot} / ${hotCap}`}
          detail={`冷却 ${cool}`}
          loading={loading}
        />
        <MetricCard
          icon={<KeyRound />}
          label="令牌"
          value={`${tokEn} / ${tokTotal}`}
          detail={`耗尽 ${tokEx}`}
          loading={loading}
        />
      </div>

      <section className="space-y-3">
        <h2 className="text-sm font-medium">使用概览</h2>
        <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
          <OverviewCard
            title="账号结构"
            value={formatNumber(total)}
            sub={`启用 ${s?.catalog_enabled ?? "—"} · 隔离 ${s?.catalog_quarantine ?? s?.pool_quarantine_count ?? "—"} · 禁用 ${s?.catalog_disabled ?? "—"}`}
            loading={loading}
          />
          <OverviewCard
            title="热池占用"
            value={formatNumber(hot)}
            sub={`容量 ${hotCap} · 冷却 ${cool}`}
            loading={loading}
          />
          <OverviewCard
            title="刷新"
            value={formatNumber((s?.refresh_ok_total ?? 0) + (s?.refresh_fail_total ?? 0))}
            sub={`OK ${s?.refresh_ok_total ?? 0} · Fail ${s?.refresh_fail_total ?? 0}`}
            loading={loading}
          />
          <OverviewCard
            title="换号 / 熔断"
            value={formatNumber(s?.pool_failover_total ?? 0)}
            sub={`failover ${s?.pool_failover_total ?? 0} · 429熔断 ${s?.pool_rate_limit_break_total ?? 0}`}
            loading={loading}
          />
          <OverviewCard
            title="Sticky 命中"
            value={
              s?.sticky_hit_rate != null
                ? formatPercent(s.sticky_hit_rate)
                : "—"
            }
            sub={`主 ${s?.sticky_primary_hits ?? 0} · 次 ${s?.sticky_secondary_hits ?? 0} · 重选 ${s?.sticky_reselects ?? 0}`}
            loading={loading}
          />
          <OverviewCard
            title="并发 / 拒绝"
            value={formatNumber(s?.proxy_inflight ?? 0)}
            sub={`inflight · 拒绝 ${s?.proxy_reject_total ?? 0} · 全局上限 ${s?.max_concurrent ?? "—"}`}
            loading={loading}
          />
          <OverviewCard
            title="进程"
            value={formatBytes(s?.process_sys_bytes ?? s?.process_rss_bytes)}
            sub={`goroutines ${s?.go_goroutines ?? "—"}`}
            loading={loading}
          />
          <OverviewCard
            title="令牌"
            value={formatNumber(tokTotal)}
            sub={`启用 ${tokEn} · 耗尽 ${tokEx}`}
            loading={loading}
          />
        </div>
      </section>
    </div>
  );
}

function MetricCard({
  icon,
  label,
  value,
  detail,
  loading,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  detail: string;
  loading: boolean;
}) {
  return (
    <div className="min-h-28 rounded-lg border border-border bg-card p-4">
      <div className="flex items-center justify-between gap-3">
        <span className="text-xs text-muted-foreground">{label}</span>
        <span className="flex size-5 items-center justify-center text-muted-foreground [&_svg]:size-4">
          {icon}
        </span>
      </div>
      <div className="mt-3 flex min-h-7 items-center text-xl font-medium tabular-nums">
        {loading ? <Spinner /> : value}
      </div>
      <p className={cn("mt-1 text-xs text-muted-foreground", loading && "invisible")}>{detail}</p>
    </div>
  );
}

function OverviewCard({
  title,
  value,
  sub,
  loading,
}: {
  title: string;
  value: string;
  sub: string;
  loading: boolean;
}) {
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <div className="text-xs text-muted-foreground">{title}</div>
      <div className="mt-2 text-lg font-medium tabular-nums">
        {loading ? <Spinner /> : value}
      </div>
      <p className={cn("mt-1 text-xs text-muted-foreground", loading && "invisible")}>{sub}</p>
    </div>
  );
}

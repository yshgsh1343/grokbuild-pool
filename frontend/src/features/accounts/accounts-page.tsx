import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Download,
  MoreHorizontal,
  RefreshCw,
  Search,
  Trash2,
} from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Spinner } from "@/components/ui/spinner";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, apiBlob, ApiError } from "@/shared/api/client";
import type {
  AccountSummary,
  AccountsListResponse,
  BatchResult,
  ModelCooldown,
} from "@/shared/api/types";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "@/shared/components/data-state";
import { DataTableShell } from "@/shared/components/data-table-shell";
import { PageHeader } from "@/shared/components/page-header";
import {
  formatDuration,
  formatNumber,
  formatPercent,
  formatUnix,
} from "@/shared/lib/format";

type Filters = {
  status: string;
  lifecycle: string;
  q: string;
};

function listAccounts(params: {
  cursor: string;
  limit: number;
  filters: Filters;
}) {
  const sp = new URLSearchParams();
  sp.set("limit", String(params.limit));
  if (params.cursor) sp.set("cursor", params.cursor);
  if (params.filters.status) sp.set("status", params.filters.status);
  if (params.filters.lifecycle) sp.set("lifecycle", params.filters.lifecycle);
  if (params.filters.q) sp.set("q", params.filters.q);
  return api<AccountsListResponse>(`/admin/accounts?${sp.toString()}`);
}

export function AccountsPage() {
  const qc = useQueryClient();
  const [limit, setLimit] = useState(50);
  const [cursor, setCursor] = useState("");
  const [cursorStack, setCursorStack] = useState<string[]>([]);
  const [pageIndex, setPageIndex] = useState(1);
  const [draft, setDraft] = useState<Filters>({ status: "", lifecycle: "", q: "" });
  const [filters, setFilters] = useState<Filters>({ status: "", lifecycle: "", q: "" });
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [detail, setDetail] = useState<AccountSummary | null>(null);
  const [deleteIds, setDeleteIds] = useState<string[] | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState("");

  const query = useQuery({
    queryKey: ["accounts", cursor, limit, filters],
    queryFn: () => listAccounts({ cursor, limit, filters }),
  });

  const accounts = query.data?.accounts ?? [];
  const total = query.data?.total ?? 0;
  const nextCursor = query.data?.next_cursor ?? "";
  const stats = query.data?.stats;
  const page = query.data?.page;
  const totalPages = total > 0 ? Math.max(1, Math.ceil(total / limit)) : 0;

  const allChecked = accounts.length > 0 && accounts.every((a) => selected.has(a.id));

  function resetPager() {
    setCursor("");
    setCursorStack([]);
    setPageIndex(1);
    setSelected(new Set());
  }

  function applyFilters() {
    setFilters({ ...draft });
    resetPager();
  }

  function resetFilters() {
    const empty = { status: "", lifecycle: "", q: "" };
    setDraft(empty);
    setFilters(empty);
    resetPager();
  }

  const batchMutation = useMutation({
    mutationFn: (body: { action: string; ids: string[] }) =>
      api<BatchResult>("/admin/accounts/batch", { method: "POST", body }),
    onSuccess: (res, vars) => {
      toast.success(`${vars.action} 完成：ok ${res.ok ?? 0} · fail ${res.failed ?? 0}`);
      setSelected(new Set());
      void qc.invalidateQueries({ queryKey: ["accounts"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const probeMutation = useMutation({
    mutationFn: async (ids: string[]) => {
      if (ids.length === 1) {
        return api(`/admin/accounts/${encodeURIComponent(ids[0])}/probe`, {
          method: "POST",
          body: {},
        });
      }
      return api("/admin/accounts/probe", {
        method: "POST",
        body: { ids: ids.slice(0, 100) },
      });
    },
    onSuccess: () => {
      toast.success("测活完成");
      void qc.invalidateQueries({ queryKey: ["accounts"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  async function exportAccounts() {
    try {
      const { blob, filename } = await apiBlob(
        "/admin/accounts/export?format=json&chunk=500",
        { dualAuth: true },
      );
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename || `accounts-export-${Date.now()}.json`;
      a.click();
      URL.revokeObjectURL(url);
      toast.success("已开始下载导出");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "导出失败");
    }
  }

  const selectedIds = useMemo(() => Array.from(selected), [selected]);

  return (
    <div className="space-y-4">
      <PageHeader
        title="账号"
        description="号池指标 · 额度/测活 · 存活/成功率 · 批量操作"
        actions={
          <>
            <Button variant="secondary" size="sm" onClick={() => void exportAccounts()}>
              <Download /> 导出 JSON
            </Button>
            <Button
              variant="ghost"
              size="icon"
              onClick={() => void query.refetch()}
              disabled={query.isFetching}
              aria-label="刷新"
            >
              <RefreshCw className={query.isFetching ? "animate-spin" : undefined} />
            </Button>
          </>
        }
      />

      {stats ? (
        <div className="flex flex-wrap gap-3 text-xs text-muted-foreground">
          <span>
            启用 <strong className="text-foreground">{stats.enabled ?? "—"}</strong>
          </span>
          <span>
            活跃 <strong className="text-foreground">{stats.active ?? "—"}</strong>
          </span>
          <span>
            冷却 <strong className="text-foreground">{stats.cooldown ?? "—"}</strong>
          </span>
          <span>
            隔离 <strong className="text-foreground">{stats.quarantine ?? "—"}</strong>
          </span>
          <span>
            禁用 <strong className="text-foreground">{stats.disabled ?? "—"}</strong>
          </span>
          {page ? (
            <>
              <span>
                本页存活{" "}
                <strong className="text-foreground">
                  {page.alive ?? 0}/{page.count ?? 0}
                </strong>
              </span>
              <span>
                inflight <strong className="text-foreground">{page.inflight_sum ?? 0}</strong>
              </span>
              <span>
                额度快照 <strong className="text-foreground">{page.with_billing ?? 0}</strong>
              </span>
            </>
          ) : null}
        </div>
      ) : null}

      <div className="flex flex-wrap items-end gap-2 rounded-lg border border-border bg-card p-3">
        <div className="space-y-1">
          <div className="text-[11px] text-muted-foreground">状态</div>
          <Select
            value={draft.status || "__all"}
            onValueChange={(v) => setDraft((d) => ({ ...d, status: v === "__all" ? "" : v }))}
          >
            <SelectTrigger className="w-36">
              <SelectValue placeholder="全部" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all">全部</SelectItem>
              <SelectItem value="alive">存活</SelectItem>
              <SelectItem value="dead">不可用</SelectItem>
              <SelectItem value="probe_ok">测活OK</SelectItem>
              <SelectItem value="probe_fail">测活失败</SelectItem>
              <SelectItem value="unprobed">未测活</SelectItem>
              <SelectItem value="enabled">启用</SelectItem>
              <SelectItem value="disabled">禁用</SelectItem>
              <SelectItem value="cooldown">冷却</SelectItem>
              <SelectItem value="quarantine">隔离</SelectItem>
              <SelectItem value="no_token">无令牌</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1">
          <div className="text-[11px] text-muted-foreground">生命周期</div>
          <Select
            value={draft.lifecycle || "__all"}
            onValueChange={(v) =>
              setDraft((d) => ({ ...d, lifecycle: v === "__all" ? "" : v }))
            }
          >
            <SelectTrigger className="w-36">
              <SelectValue placeholder="全部" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all">全部</SelectItem>
              <SelectItem value="active">active</SelectItem>
              <SelectItem value="quarantined">quarantined</SelectItem>
              <SelectItem value="purged">purged</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="min-w-[180px] flex-1 space-y-1">
          <div className="text-[11px] text-muted-foreground">搜索</div>
          <div className="relative">
            <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="pl-7"
              placeholder="ID / email / name"
              value={draft.q}
              onChange={(e) => setDraft((d) => ({ ...d, q: e.target.value }))}
              onKeyDown={(e) => {
                if (e.key === "Enter") applyFilters();
              }}
            />
          </div>
        </div>
        <Button size="sm" onClick={applyFilters}>
          应用
        </Button>
        <Button size="sm" variant="secondary" onClick={resetFilters}>
          重置
        </Button>
      </div>

      <DataTableShell
        toolbar={
          <div className="flex w-full flex-wrap items-center gap-2">
            <Button
              size="sm"
              variant="secondary"
              onClick={() =>
                setSelected(new Set(accounts.map((a) => a.id)))
              }
              disabled={!accounts.length}
            >
              全选本页
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setSelected(new Set())}
              disabled={!selected.size}
            >
              清空
            </Button>
            <span className="text-xs text-muted-foreground">已选 {selected.size}</span>
            <div className="flex-1" />
            <Button
              size="sm"
              variant="secondary"
              disabled={!selected.size || batchMutation.isPending}
              onClick={() =>
                batchMutation.mutate({ action: "enable", ids: selectedIds })
              }
            >
              批量启用
            </Button>
            <Button
              size="sm"
              variant="secondary"
              disabled={!selected.size || batchMutation.isPending}
              onClick={() =>
                batchMutation.mutate({ action: "disable", ids: selectedIds })
              }
            >
              批量禁用
            </Button>
            <Button
              size="sm"
              variant="secondary"
              disabled={!selected.size || probeMutation.isPending}
              onClick={() => {
                if (selectedIds.length > 100) {
                  toast.warning("批量测活最多 100 个，将截取前 100");
                }
                probeMutation.mutate(selectedIds);
              }}
            >
              批量测活
            </Button>
            <Button
              size="sm"
              variant="destructive"
              disabled={!selected.size || batchMutation.isPending}
              onClick={() => {
                setDeleteConfirm("");
                setDeleteIds(selectedIds);
              }}
            >
              <Trash2 /> 批量删除
            </Button>
            <Select
              value={String(limit)}
              onValueChange={(v) => {
                setLimit(Number(v));
                resetPager();
              }}
            >
              <SelectTrigger className="w-24">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {[20, 50, 100, 200].map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {n}/页
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        }
        footer={
          <div className="flex w-full flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
            <span>
              {totalPages > 0
                ? `第 ${pageIndex} / ${totalPages} 页`
                : `第 ${pageIndex} 页`}
              {total > 0 ? ` · 共 ${total} 账号` : ""}
              {` · 本页 ${accounts.length} 条`}
              {nextCursor ? " · 有后续" : accounts.length > 0 ? " · 已到末页" : ""}
            </span>
            <div className="flex gap-2">
              <Button
                size="sm"
                variant="secondary"
                disabled={!cursorStack.length || query.isFetching}
                onClick={() => {
                  const prev = [...cursorStack];
                  const last = prev.pop() ?? "";
                  setCursorStack(prev);
                  setCursor(last);
                  setPageIndex((p) => Math.max(1, p - 1));
                  setSelected(new Set());
                }}
              >
                上一页
              </Button>
              <Button
                size="sm"
                variant="secondary"
                disabled={!nextCursor || query.isFetching}
                onClick={() => {
                  setCursorStack((s) => [...s, cursor]);
                  setCursor(nextCursor);
                  setPageIndex((p) => p + 1);
                  setSelected(new Set());
                }}
              >
                下一页
              </Button>
            </div>
          </div>
        }
      >
        {query.isPending ? (
          <LoadingState />
        ) : query.isError ? (
          <div className="p-4">
            <ErrorState
              message={
                query.error instanceof ApiError
                  ? query.error.message
                  : "加载失败"
              }
              onRetry={() => void query.refetch()}
            />
          </div>
        ) : !accounts.length ? (
          <EmptyState
            title="暂无账号"
            description="可通过导入任务上传 SSO / JSON，或使用 poolctl 导入"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8">
                  <Checkbox
                    checked={allChecked}
                    onCheckedChange={(v) => {
                      if (v) setSelected(new Set(accounts.map((a) => a.id)));
                      else setSelected(new Set());
                    }}
                  />
                </TableHead>
                <TableHead>ID</TableHead>
                <TableHead>Email</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>成功率</TableHead>
                <TableHead>并发</TableHead>
                <TableHead>额度 / 测活</TableHead>
                <TableHead>代理</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {accounts.map((a) => (
                <TableRow key={a.id} data-state={selected.has(a.id) ? "selected" : undefined}>
                  <TableCell>
                    <Checkbox
                      checked={selected.has(a.id)}
                      onCheckedChange={(v) => {
                        setSelected((prev) => {
                          const next = new Set(prev);
                          if (v) next.add(a.id);
                          else next.delete(a.id);
                          return next;
                        });
                      }}
                    />
                  </TableCell>
                  <TableCell>
                    <button
                      type="button"
                      className="mono text-left hover:underline"
                      onClick={() => setDetail(a)}
                    >
                      {a.id}
                    </button>
                  </TableCell>
                  <TableCell className="max-w-[140px] truncate">{a.email || "—"}</TableCell>
                  <TableCell>
                    <StatusBadges a={a} />
                  </TableCell>
                  <TableCell>{formatPercent(a.success_rate ?? null)}</TableCell>
                  <TableCell className="mono">{a.inflight ?? 0}</TableCell>
                  <TableCell>
                    <BillingCell a={a} />
                  </TableCell>
                  <TableCell className="mono max-w-[120px] truncate" title={a.proxy_url || "直连"}>
                    {a.proxy_url || "直连"}
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => setDetail(a)}
                        title="详情"
                      >
                        <MoreHorizontal />
                      </Button>
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={probeMutation.isPending}
                        onClick={() => probeMutation.mutate([a.id])}
                      >
                        测活
                      </Button>
                      {a.enabled === false || a.manual_disabled ? (
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() =>
                            batchMutation.mutate({ action: "enable", ids: [a.id] })
                          }
                        >
                          启用
                        </Button>
                      ) : (
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() =>
                            batchMutation.mutate({ action: "disable", ids: [a.id] })
                          }
                        >
                          禁用
                        </Button>
                      )}
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => {
                          setDeleteConfirm("");
                          setDeleteIds([a.id]);
                        }}
                      >
                        <Trash2 className="text-destructive" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </DataTableShell>

      <AccountDetailSheet account={detail} onClose={() => setDetail(null)} />

      <AlertDialog
        open={!!deleteIds}
        onOpenChange={(open) => {
          if (!open) {
            setDeleteIds(null);
            setDeleteConfirm("");
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认删除账号</AlertDialogTitle>
            <AlertDialogDescription>
              将删除 {deleteIds?.length ?? 0} 个账号，不可恢复。请输入{" "}
              <strong>DELETE</strong> 确认。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <Input
            value={deleteConfirm}
            onChange={(e) => setDeleteConfirm(e.target.value)}
            placeholder="DELETE"
            autoFocus
          />
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              disabled={deleteConfirm !== "DELETE" || batchMutation.isPending}
              onClick={(e) => {
                e.preventDefault();
                if (!deleteIds) return;
                batchMutation.mutate(
                  { action: "delete", ids: deleteIds },
                  {
                    onSuccess: () => {
                      setDeleteIds(null);
                      setDeleteConfirm("");
                    },
                  },
                );
              }}
            >
              删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function StatusBadges({ a }: { a: AccountSummary }) {
  return (
    <div className="flex flex-wrap gap-1">
      <Badge variant={a.alive ? "success" : "danger"}>{a.alive ? "存活" : "不可用"}</Badge>
      {a.billing?.probe_ok === true ? <Badge variant="success">测活OK</Badge> : null}
      {a.billing?.probe_ok === false ? <Badge variant="danger">测活失败</Badge> : null}
      {a.lifecycle === "quarantined" ? <Badge variant="warning">隔离</Badge> : null}
      {a.lifecycle === "purged" ? <Badge variant="outline">已清理</Badge> : null}
      {a.enabled === false || a.manual_disabled ? (
        <Badge variant="outline">禁用</Badge>
      ) : (
        <Badge variant="default">启用</Badge>
      )}
      {(a.cooldown_remaining_sec ?? 0) > 0 ? (
        <Badge variant="warning">冷却 {formatDuration(a.cooldown_remaining_sec)}</Badge>
      ) : null}
    </div>
  );
}

function BillingCell({ a }: { a: AccountSummary }) {
  const b = a.billing;
  if (!b) return <span className="text-muted-foreground">未测活</span>;
  const parts: string[] = [];
  if (b.monthly_used != null || b.monthly_limit != null) {
    parts.push(`月 ${formatNumber(b.monthly_used ?? 0)}/${formatNumber(b.monthly_limit ?? 0)}`);
  }
  if (b.weekly_usage_percent != null) {
    parts.push(`周 ${Number(b.weekly_usage_percent).toFixed(1)}%`);
  }
  if (b.grok_build_percent != null) {
    parts.push(`Build ${Number(b.grok_build_percent).toFixed(1)}%`);
  }
  return (
    <div className="text-[11px] leading-relaxed">
      {parts.map((p) => (
        <div key={p}>{p}</div>
      ))}
      {b.probed_at ? (
        <div className="text-muted-foreground">{formatUnix(b.probed_at)}</div>
      ) : null}
    </div>
  );
}

function AccountDetailSheet({
  account,
  onClose,
}: {
  account: AccountSummary | null;
  onClose: () => void;
}) {
  const coolQ = useQuery({
    queryKey: ["model-cooldowns", account?.id],
    enabled: !!account?.id,
    queryFn: () =>
      api<{ model_cooldowns?: ModelCooldown[] }>(
        `/admin/accounts/${encodeURIComponent(account!.id)}/model-cooldowns`,
      ),
  });

  return (
    <Sheet open={!!account} onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="bg-card">
        <SheetHeader>
          <SheetTitle className="mono">{account?.id}</SheetTitle>
          <SheetDescription>
            {(account?.email || "—") + " · " + (account?.lifecycle || "—")}
          </SheetDescription>
        </SheetHeader>
        {account ? (
          <div className="space-y-5 overflow-y-auto px-4 pb-6">
            <section>
              <div className="mb-2 text-xs font-medium">状态</div>
              <StatusBadges a={account} />
              {account.status_reason ? (
                <p className="mt-2 text-xs text-muted-foreground">{account.status_reason}</p>
              ) : null}
            </section>
            <section>
              <div className="mb-2 text-xs font-medium">基础信息</div>
              <dl className="grid grid-cols-[80px_1fr] gap-y-2 text-xs">
                <dt className="text-muted-foreground">优先级</dt>
                <dd>{account.priority ?? 0}</dd>
                <dt className="text-muted-foreground">成功率</dt>
                <dd>{formatPercent(account.success_rate ?? null)}</dd>
                <dt className="text-muted-foreground">并发</dt>
                <dd className="mono">{account.inflight ?? 0}</dd>
                <dt className="text-muted-foreground">代理</dt>
                <dd className="mono break-all">{account.proxy_url || "直连"}</dd>
                <dt className="text-muted-foreground">last_error</dt>
                <dd className="break-all">{account.last_error || "—"}</dd>
              </dl>
            </section>
            <section>
              <div className="mb-2 text-xs font-medium">额度 / 测活</div>
              <BillingCell a={account} />
            </section>
            <section>
              <div className="mb-2 text-xs font-medium">模型冷却</div>
              {coolQ.isPending ? (
                <div className="flex items-center gap-2 text-xs text-muted-foreground">
                  <Spinner /> 加载中…
                </div>
              ) : coolQ.isError ? (
                <p className="text-xs text-destructive">
                  {coolQ.error instanceof Error ? coolQ.error.message : "加载失败"}
                </p>
              ) : !(coolQ.data?.model_cooldowns ?? []).length ? (
                <p className="text-xs text-muted-foreground">当前无模型冷却</p>
              ) : (
                <ul className="space-y-2">
                  {(coolQ.data?.model_cooldowns ?? []).map((r) => (
                    <li
                      key={`${r.model}-${r.cooldown_until}`}
                      className="rounded-md border border-border px-3 py-2"
                    >
                      <div className="mono text-xs">{r.model || "?"}</div>
                      <div className="mt-0.5 text-[11px] text-muted-foreground">
                        剩余 {formatDuration(r.remaining_sec)}
                        {r.last_error ? ` · ${r.last_error}` : ""}
                        {r.cooldown_until ? ` · until ${formatUnix(r.cooldown_until)}` : ""}
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </div>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

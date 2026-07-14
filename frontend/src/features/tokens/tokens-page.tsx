import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Copy, Plus, Trash2 } from "lucide-react";
import { Fragment, useState } from "react";
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
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api, ApiError } from "@/shared/api/client";
import type {
  BatchResult,
  SettingsSnapshot,
  Token,
  TokensCreateResponse,
  TokensListResponse,
} from "@/shared/api/types";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "@/shared/components/data-state";
import { DataTableShell } from "@/shared/components/data-table-shell";
import { PageHeader } from "@/shared/components/page-header";
import { copyText, formatNumber } from "@/shared/lib/format";

export function TokensPage() {
  const qc = useQueryClient();
  const settingsQ = useQuery({
    queryKey: ["settings"],
    queryFn: () => api<SettingsSnapshot>("/admin/settings"),
    staleTime: 60_000,
  });
  const defaults = settingsQ.data;

  const [name, setName] = useState("client");
  const [count, setCount] = useState(1);
  const [remainQuota, setRemainQuota] = useState<number | null>(null);
  const [unlimited, setUnlimited] = useState<boolean | null>(null);
  const [maxConcurrent, setMaxConcurrent] = useState<number | null>(null);
  const [rpm, setRpm] = useState<number | null>(null);
  const [onceKeys, setOnceKeys] = useState<string[]>([]);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [deleteIds, setDeleteIds] = useState<string[] | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState("");

  // Edit form state for expanded row
  const [editName, setEditName] = useState("");
  const [editQuota, setEditQuota] = useState(0);
  const [editUnlimited, setEditUnlimited] = useState(false);
  const [editConc, setEditConc] = useState(0);
  const [editRpm, setEditRpm] = useState(0);

  const listQ = useQuery({
    queryKey: ["tokens"],
    queryFn: () => api<TokensListResponse>("/admin/tokens"),
  });
  const tokens = listQ.data?.tokens ?? [];

  const defQ = remainQuota ?? defaults?.token_default_remain_quota ?? 1000;
  const defU = unlimited ?? defaults?.token_default_unlimited ?? false;
  const defC = maxConcurrent ?? defaults?.token_default_max_concurrent ?? 5;
  const defR = rpm ?? defaults?.token_default_rpm ?? 0;

  const createM = useMutation({
    mutationFn: () =>
      api<TokensCreateResponse>("/admin/tokens", {
        method: "POST",
        body: {
          name: name || "client",
          count: Math.min(100, Math.max(1, count || 1)),
          remain_quota: defQ,
          unlimited_quota: defU,
          max_concurrent: defC,
          rpm: defR,
        },
      }),
    onSuccess: async (res) => {
      const keys: string[] = [];
      for (const item of res.tokens ?? []) {
        const k = item.api_key || item.plaintext;
        if (k) keys.push(k);
      }
      if (!keys.length) {
        if (res.api_key) keys.push(res.api_key);
        else if (res.plaintext) keys.push(res.plaintext);
      }
      setOnceKeys(keys);
      if (keys.length) {
        try {
          await copyText(keys.join("\n"));
          toast.success(`已创建 ${keys.length} 把密钥，并复制到剪贴板`);
        } catch {
          toast.success(`已创建 ${keys.length} 把密钥（复制失败，请手动复制）`);
        }
      } else {
        toast.success("已创建，但响应中未包含明文密钥");
      }
      void qc.invalidateQueries({ queryKey: ["tokens"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const patchM = useMutation({
    mutationFn: (body: {
      id: string;
      name: string;
      remain_quota: number;
      unlimited_quota: boolean;
      max_concurrent: number;
      rpm: number;
    }) =>
      api(`/admin/tokens/${encodeURIComponent(body.id)}`, {
        method: "PATCH",
        body: {
          name: body.name,
          remain_quota: body.remain_quota,
          unlimited_quota: body.unlimited_quota,
          max_concurrent: body.max_concurrent,
          rpm: body.rpm,
        },
      }),
    onSuccess: () => {
      toast.success("已保存（下一请求立即生效）");
      void qc.invalidateQueries({ queryKey: ["tokens"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const actionM = useMutation({
    mutationFn: (p: { id: string; action: "enable" | "disable" | "delete" }) => {
      if (p.action === "delete") {
        return api(`/admin/tokens/${encodeURIComponent(p.id)}`, { method: "DELETE" });
      }
      return api(`/admin/tokens/${encodeURIComponent(p.id)}/${p.action}`, {
        method: "POST",
        body: {},
      });
    },
    onSuccess: () => {
      toast.success("已更新");
      void qc.invalidateQueries({ queryKey: ["tokens"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const batchDeleteM = useMutation({
    mutationFn: (ids: string[]) =>
      api<BatchResult>("/admin/tokens/batch", {
        method: "POST",
        body: { action: "delete", ids },
      }),
    onSuccess: (res) => {
      toast.success(`已删除 ${res.deleted ?? res.ok ?? 0}`);
      setSelected(new Set());
      void qc.invalidateQueries({ queryKey: ["tokens"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  function openEdit(t: Token) {
    setExpanded(t.id);
    setEditName(t.name ?? "");
    setEditQuota(t.remain_quota ?? 0);
    setEditUnlimited(!!t.unlimited_quota);
    setEditConc(t.max_concurrent ?? 0);
    setEditRpm(t.rpm ?? 0);
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="Token"
        description="创建 / 编辑并发·RPM·额度 · 明文仅创建时显示一次 · PATCH 立即生效"
      />

      <div className="rounded-lg border border-border bg-card p-4">
        <div className="mb-3 text-sm font-medium">快速创建</div>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
          <div className="space-y-1">
            <Label>名称</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="space-y-1">
            <Label>数量 (1–100)</Label>
            <Input
              type="number"
              min={1}
              max={100}
              value={count}
              onChange={(e) => setCount(Number(e.target.value) || 1)}
            />
          </div>
          <div className="space-y-1">
            <Label>剩余额度</Label>
            <Input
              type="number"
              min={0}
              value={defQ}
              onChange={(e) => setRemainQuota(Number(e.target.value) || 0)}
              disabled={defU}
            />
          </div>
          <div className="space-y-1">
            <Label>无限额度</Label>
            <div className="flex h-8 items-center gap-2">
              <Checkbox
                checked={defU}
                onCheckedChange={(v) => setUnlimited(!!v)}
              />
              <span className="text-xs text-muted-foreground">unlimited</span>
            </div>
          </div>
          <div className="space-y-1">
            <Label>并发 (0=不限)</Label>
            <Input
              type="number"
              min={0}
              value={defC}
              onChange={(e) => setMaxConcurrent(Number(e.target.value) || 0)}
            />
          </div>
          <div className="space-y-1">
            <Label>RPM (0=不限)</Label>
            <Input
              type="number"
              min={0}
              value={defR}
              onChange={(e) => setRpm(Number(e.target.value) || 0)}
            />
          </div>
        </div>
        <div className="mt-3">
          <Button
            onClick={() => createM.mutate()}
            disabled={createM.isPending}
          >
            <Plus /> {createM.isPending ? "创建中…" : "创建并复制密钥"}
          </Button>
        </div>

        {onceKeys.length > 0 ? (
          <div className="mt-4 rounded-md border border-warning/30 bg-warning/5 p-3">
            <div className="mb-2 flex items-center justify-between gap-2">
              <p className="text-xs text-muted-foreground">
                明文密钥仅显示一次（共 {onceKeys.length} 把），请立即复制：
              </p>
              <Button
                size="sm"
                variant="secondary"
                onClick={() =>
                  void copyText(onceKeys.join("\n"))
                    .then(() => toast.success("已全部复制"))
                    .catch(() => toast.error("复制失败"))
                }
              >
                <Copy /> 全部复制
              </Button>
            </div>
            <div className="space-y-1">
              {onceKeys.map((k, i) => (
                <div key={i} className="flex items-center gap-2">
                  <code className="mono flex-1 break-all rounded bg-muted px-2 py-1">
                    {k}
                  </code>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() =>
                      void copyText(k)
                        .then(() => toast.success(`已复制第 ${i + 1} 把`))
                        .catch(() => toast.error("复制失败"))
                    }
                  >
                    <Copy />
                  </Button>
                </div>
              ))}
            </div>
          </div>
        ) : null}
      </div>

      <DataTableShell
        toolbar={
          <div className="flex w-full flex-wrap items-center gap-2">
            <span className="text-xs text-muted-foreground">
              共 {tokens.length} 个令牌 · 已选 {selected.size}
            </span>
            <div className="flex-1" />
            <Button
              size="sm"
              variant="destructive"
              disabled={!selected.size || batchDeleteM.isPending}
              onClick={() => {
                setDeleteConfirm("");
                setDeleteIds(Array.from(selected));
              }}
            >
              <Trash2 /> 批量删除
            </Button>
          </div>
        }
      >
        {listQ.isPending ? (
          <LoadingState />
        ) : listQ.isError ? (
          <div className="p-4">
            <ErrorState
              message={
                listQ.error instanceof ApiError
                  ? listQ.error.message
                  : "加载失败"
              }
              onRetry={() => void listQ.refetch()}
            />
          </div>
        ) : !tokens.length ? (
          <EmptyState title="暂无令牌" description="使用上方表单快速创建" />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8">
                  <Checkbox
                    checked={tokens.length > 0 && tokens.every((t) => selected.has(t.id))}
                    onCheckedChange={(v) => {
                      if (v) setSelected(new Set(tokens.map((t) => t.id)));
                      else setSelected(new Set());
                    }}
                  />
                </TableHead>
                <TableHead className="w-8" />
                <TableHead>ID</TableHead>
                <TableHead>名称</TableHead>
                <TableHead>前缀</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>额度</TableHead>
                <TableHead>并发</TableHead>
                <TableHead>RPM</TableHead>
                <TableHead>已用/请求</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tokens.map((t) => {
                const open = expanded === t.id;
                return (
                  <Fragment key={t.id}>
                    <TableRow>
                      <TableCell>
                        <Checkbox
                          checked={selected.has(t.id)}
                          onCheckedChange={(v) => {
                            setSelected((prev) => {
                              const next = new Set(prev);
                              if (v) next.add(t.id);
                              else next.delete(t.id);
                              return next;
                            });
                          }}
                        />
                      </TableCell>
                      <TableCell>
                        <Button
                          size="icon"
                          variant="ghost"
                          className="size-7"
                          onClick={() => (open ? setExpanded(null) : openEdit(t))}
                        >
                          {open ? <ChevronDown /> : <ChevronRight />}
                        </Button>
                      </TableCell>
                      <TableCell className="mono">{t.id}</TableCell>
                      <TableCell>{t.name || "—"}</TableCell>
                      <TableCell className="mono">{t.key_prefix || "—"}</TableCell>
                      <TableCell>
                        <Badge variant={t.enabled ? "success" : "outline"}>
                          {t.enabled ? "启用" : "禁用"}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        {t.unlimited_quota ? "∞" : formatNumber(t.remain_quota ?? 0)}
                      </TableCell>
                      <TableCell className="mono">
                        {t.max_concurrent
                          ? `${t.inflight ?? 0} / ${t.max_concurrent}`
                          : `不限 · ${t.inflight ?? 0}`}
                      </TableCell>
                      <TableCell className="mono">{t.rpm || "不限"}</TableCell>
                      <TableCell className="mono">
                        {formatNumber(t.used_quota ?? 0)} / {formatNumber(t.request_count ?? 0)}
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1">
                          {t.enabled ? (
                            <Button
                              size="sm"
                              variant="secondary"
                              onClick={() =>
                                actionM.mutate({ id: t.id, action: "disable" })
                              }
                            >
                              禁用
                            </Button>
                          ) : (
                            <Button
                              size="sm"
                              variant="secondary"
                              onClick={() =>
                                actionM.mutate({ id: t.id, action: "enable" })
                              }
                            >
                              启用
                            </Button>
                          )}
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => {
                              setDeleteConfirm("");
                              setDeleteIds([t.id]);
                            }}
                          >
                            <Trash2 className="text-destructive" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                    {open ? (
                      <TableRow>
                        <TableCell colSpan={11} className="bg-muted/40">
                          <div className="grid gap-3 p-2 sm:grid-cols-2 lg:grid-cols-5">
                            <div className="space-y-1">
                              <Label>名称</Label>
                              <Input
                                value={editName}
                                onChange={(e) => setEditName(e.target.value)}
                              />
                            </div>
                            <div className="space-y-1">
                              <Label>剩余额度</Label>
                              <Input
                                type="number"
                                min={0}
                                value={editQuota}
                                disabled={editUnlimited}
                                onChange={(e) =>
                                  setEditQuota(Number(e.target.value) || 0)
                                }
                              />
                            </div>
                            <div className="space-y-1">
                              <Label>无限额度</Label>
                              <div className="flex h-8 items-center gap-2">
                                <Checkbox
                                  checked={editUnlimited}
                                  onCheckedChange={(v) => setEditUnlimited(!!v)}
                                />
                              </div>
                            </div>
                            <div className="space-y-1">
                              <Label>并发 (0=不限)</Label>
                              <Input
                                type="number"
                                min={0}
                                value={editConc}
                                onChange={(e) =>
                                  setEditConc(Number(e.target.value) || 0)
                                }
                              />
                            </div>
                            <div className="space-y-1">
                              <Label>RPM (0=不限)</Label>
                              <Input
                                type="number"
                                min={0}
                                value={editRpm}
                                onChange={(e) =>
                                  setEditRpm(Number(e.target.value) || 0)
                                }
                              />
                            </div>
                          </div>
                          <p className="px-2 text-[11px] text-muted-foreground">
                            保存后下一请求立即按新并发/RPM 限流；当前占用 {t.inflight ?? 0}
                          </p>
                          <div className="flex gap-2 p-2">
                            <Button
                              size="sm"
                              disabled={patchM.isPending}
                              onClick={() =>
                                patchM.mutate({
                                  id: t.id,
                                  name: editName,
                                  remain_quota: editQuota,
                                  unlimited_quota: editUnlimited,
                                  max_concurrent: editConc,
                                  rpm: editRpm,
                                })
                              }
                            >
                              保存
                            </Button>
                            <Button
                              size="sm"
                              variant="secondary"
                              onClick={() => setExpanded(null)}
                            >
                              收起
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ) : null}
                  </Fragment>
                );
              })}
            </TableBody>
          </Table>
        )}
      </DataTableShell>

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
            <AlertDialogTitle>确认删除令牌</AlertDialogTitle>
            <AlertDialogDescription>
              将删除 {deleteIds?.length ?? 0} 个令牌。请输入 <strong>DELETE</strong> 确认。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <Input
            value={deleteConfirm}
            onChange={(e) => setDeleteConfirm(e.target.value)}
            placeholder="DELETE"
          />
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              disabled={deleteConfirm !== "DELETE"}
              onClick={(e) => {
                e.preventDefault();
                if (!deleteIds) return;
                if (deleteIds.length === 1) {
                  actionM.mutate(
                    { id: deleteIds[0], action: "delete" },
                    {
                      onSuccess: () => {
                        setDeleteIds(null);
                        setDeleteConfirm("");
                      },
                    },
                  );
                } else {
                  batchDeleteM.mutate(deleteIds, {
                    onSuccess: () => {
                      setDeleteIds(null);
                      setDeleteConfirm("");
                    },
                  });
                }
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

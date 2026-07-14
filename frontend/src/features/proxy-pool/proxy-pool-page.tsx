import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCw, Save, Shuffle, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { api, ApiError } from "@/shared/api/client";
import type {
  ProxyNode,
  ProxyPoolAssignResponse,
  ProxyPoolResponse,
} from "@/shared/api/types";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "@/shared/components/data-state";
import { DataTableShell } from "@/shared/components/data-table-shell";
import { PageHeader } from "@/shared/components/page-header";
import { formatUnix } from "@/shared/lib/format";

function emptyNode(): ProxyNode {
  return { id: "", url: "", enabled: true, weight: 1 };
}

export function ProxyPoolPage() {
  const qc = useQueryClient();
  const [nodes, setNodes] = useState<ProxyNode[]>([]);
  const [bulk, setBulk] = useState("");
  const [assignLimit, setAssignLimit] = useState(5000);
  const [assignMode, setAssignMode] = useState("hash");
  const [dryRun, setDryRun] = useState(false);

  const q = useQuery({
    queryKey: ["proxy-pool"],
    queryFn: () => api<ProxyPoolResponse>("/admin/proxy-pool"),
  });

  useEffect(() => {
    if (q.data?.nodes) {
      setNodes(q.data.nodes.map((n) => ({ ...n })));
    }
    if (q.data?.assign_mode) setAssignMode(q.data.assign_mode);
  }, [q.data]);

  const saveM = useMutation({
    mutationFn: (list: ProxyNode[]) =>
      api<ProxyPoolResponse>("/admin/proxy-pool", {
        method: "PUT",
        body: {
          nodes: list
            .map((n) => ({
              id: (n.id || "").trim(),
              url: (n.url || "").trim(),
              enabled: n.enabled !== false,
              weight: n.weight && n.weight > 0 ? n.weight : 1,
            }))
            .filter((n) => n.url),
        },
      }),
    onSuccess: (res) => {
      toast.success(`已保存 ${res.nodes?.length ?? 0} 个节点 · 健康 ${res.healthy ?? 0}`);
      void qc.invalidateQueries({ queryKey: ["proxy-pool"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const assignM = useMutation({
    mutationFn: () =>
      api<ProxyPoolAssignResponse>("/admin/proxy-pool/assign", {
        method: "POST",
        body: {
          limit: assignLimit,
          dry_run: dryRun,
          mode: assignMode,
        },
      }),
    onSuccess: (res) => {
      toast.success(
        `${res.dry_run ? "演练" : "分配"}完成：assigned ${res.assigned ?? 0} · skipped ${res.skipped ?? 0} · failed ${res.failed ?? 0}`,
      );
      void qc.invalidateQueries({ queryKey: ["proxy-pool"] });
      void qc.invalidateQueries({ queryKey: ["accounts"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  function addFromBulk() {
    const lines = bulk
      .split(/\r?\n/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (!lines.length) {
      toast.message("没有可添加的 URL");
      return;
    }
    const next = [...nodes];
    for (const line of lines) {
      // support "id url" or bare url
      const parts = line.split(/\s+/);
      let id = "";
      let url = line;
      if (parts.length >= 2 && !parts[0].includes("://")) {
        id = parts[0];
        url = parts.slice(1).join(" ");
      }
      if (next.some((n) => n.url === url)) continue;
      next.push({ id, url, enabled: true, weight: 1 });
    }
    setNodes(next);
    setBulk("");
    toast.success(`已加入 ${lines.length} 行（保存后生效）`);
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="代理池"
        description="SOCKS5/HTTP 出口节点 · 分配后写入账号 proxy_url 持久绑定 · 策略在设置页热更"
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

      {q.isPending ? (
        <LoadingState />
      ) : q.isError ? (
        <ErrorState
          message={
            q.error instanceof ApiError
              ? q.error.message
              : "加载失败（代理池可能未启用）"
          }
          onRetry={() => void q.refetch()}
        />
      ) : (
        <>
          <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
            <Stat label="启用策略" value={q.data?.enabled ? "开" : "关"} />
            <Stat label="require_proxy" value={q.data?.require_proxy ? "开" : "关"} />
            <Stat label="分配模式" value={q.data?.assign_mode || "hash"} />
            <Stat label="健康节点" value={String(q.data?.healthy ?? 0)} />
          </div>
          {q.data?.path ? (
            <p className="text-xs text-muted-foreground mono">path: {q.data.path}</p>
          ) : null}
          {q.data?.note ? (
            <p className="text-xs text-muted-foreground">{q.data.note}</p>
          ) : null}

          <div className="rounded-lg border border-border bg-card p-4 space-y-3">
            <div className="text-sm font-medium">批量添加节点</div>
            <Textarea
              rows={4}
              className="mono"
              placeholder={"每行一个 URL，可选前缀 id：\nsocks5://user:pass@1.2.3.4:1080\nnode-a http://127.0.0.1:7890"}
              value={bulk}
              onChange={(e) => setBulk(e.target.value)}
            />
            <Button size="sm" variant="secondary" onClick={addFromBulk}>
              <Plus /> 加入列表
            </Button>
          </div>

          <DataTableShell
            toolbar={
              <div className="flex w-full flex-wrap items-center gap-2">
                <span className="text-xs text-muted-foreground">
                  编辑中 {nodes.length} 个节点
                </span>
                <div className="flex-1" />
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => setNodes((n) => [...n, emptyNode()])}
                >
                  <Plus /> 空行
                </Button>
                <Button
                  size="sm"
                  disabled={saveM.isPending}
                  onClick={() => saveM.mutate(nodes)}
                >
                  <Save /> {saveM.isPending ? "保存中…" : "保存节点列表"}
                </Button>
              </div>
            }
          >
            {!nodes.length ? (
              <EmptyState
                title="暂无代理节点"
                description="粘贴 socks5:// 或 http:// 列表后保存"
              />
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>启用</TableHead>
                    <TableHead>ID</TableHead>
                    <TableHead>URL</TableHead>
                    <TableHead>权重</TableHead>
                    <TableHead>失败</TableHead>
                    <TableHead>冷却至</TableHead>
                    <TableHead>绑定数</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {nodes.map((n, i) => (
                    <TableRow key={`${n.id}-${i}`}>
                      <TableCell>
                        <Checkbox
                          checked={n.enabled !== false}
                          onCheckedChange={(v) => {
                            setNodes((list) => {
                              const next = [...list];
                              next[i] = { ...next[i], enabled: !!v };
                              return next;
                            });
                          }}
                        />
                      </TableCell>
                      <TableCell>
                        <Input
                          className="mono h-7"
                          value={n.id || ""}
                          placeholder="auto"
                          onChange={(e) => {
                            const v = e.target.value;
                            setNodes((list) => {
                              const next = [...list];
                              next[i] = { ...next[i], id: v };
                              return next;
                            });
                          }}
                        />
                      </TableCell>
                      <TableCell>
                        <Input
                          className="mono h-7 min-w-[220px]"
                          value={n.url || ""}
                          placeholder="socks5://host:1080"
                          onChange={(e) => {
                            const v = e.target.value;
                            setNodes((list) => {
                              const next = [...list];
                              next[i] = { ...next[i], url: v };
                              return next;
                            });
                          }}
                        />
                      </TableCell>
                      <TableCell>
                        <Input
                          type="number"
                          className="h-7 w-16"
                          min={1}
                          value={n.weight ?? 1}
                          onChange={(e) => {
                            const v = Number(e.target.value) || 1;
                            setNodes((list) => {
                              const next = [...list];
                              next[i] = { ...next[i], weight: v };
                              return next;
                            });
                          }}
                        />
                      </TableCell>
                      <TableCell className="mono text-xs">
                        {n.fail_count ?? 0}
                        {n.last_error ? (
                          <div className="max-w-[120px] truncate text-destructive" title={n.last_error}>
                            {n.last_error}
                          </div>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {n.cooldown_until ? formatUnix(n.cooldown_until) : "—"}
                      </TableCell>
                      <TableCell className="mono">{n.assigned_accounts ?? 0}</TableCell>
                      <TableCell className="text-right">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() =>
                            setNodes((list) => list.filter((_, j) => j !== i))
                          }
                        >
                          <Trash2 className="text-destructive" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </DataTableShell>

          <div className="rounded-lg border border-border bg-card p-4 space-y-3">
            <div className="text-sm font-medium">批量分配到无代理账号</div>
            <p className="text-xs text-muted-foreground">
              仅处理 <Badge variant="outline">proxy_url 为空</Badge> 的账号；写库持久绑定。设置页需开启「启用代理池自动分配」。
            </p>
            <div className="grid gap-3 sm:grid-cols-3">
              <div className="space-y-1">
                <Label>limit</Label>
                <Input
                  type="number"
                  min={1}
                  max={50000}
                  value={assignLimit}
                  onChange={(e) => setAssignLimit(Number(e.target.value) || 5000)}
                />
              </div>
              <div className="space-y-1">
                <Label>mode</Label>
                <Select value={assignMode} onValueChange={setAssignMode}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="hash">hash</SelectItem>
                    <SelectItem value="least_accounts">least_accounts</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="flex items-end gap-2 pb-1">
                <Checkbox
                  id="dry-run"
                  checked={dryRun}
                  onCheckedChange={(v) => setDryRun(!!v)}
                />
                <Label htmlFor="dry-run" className="cursor-pointer">
                  dry_run（只统计不写库）
                </Label>
              </div>
            </div>
            <Button
              size="sm"
              disabled={assignM.isPending}
              onClick={() => assignM.mutate()}
            >
              <Shuffle /> {assignM.isPending ? "分配中…" : "执行分配"}
            </Button>
          </div>
        </>
      )}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border bg-card p-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 text-sm font-medium">{value}</div>
    </div>
  );
}

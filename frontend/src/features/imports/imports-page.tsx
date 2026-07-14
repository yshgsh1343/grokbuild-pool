import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw, Upload } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import { api, ApiError } from "@/shared/api/client";
import type { ImportJob, ImportJobsResponse } from "@/shared/api/types";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "@/shared/components/data-state";
import { DataTableShell } from "@/shared/components/data-table-shell";
import { PageHeader } from "@/shared/components/page-header";
import { formatBytes } from "@/shared/lib/format";
import { cn } from "@/shared/lib/cn";

function phaseLabel(j: ImportJob): string {
  if (j.message) return String(j.message);
  const p = String(j.phase || "");
  if (p === "parsing") return "解析输入";
  if (p === "converting") return "SSO 换票中";
  if (p === "writing") return "写入账号库";
  if (p === "reloading") return "重建热池";
  if (p === "done") return j.state === "failed" ? "失败" : "完成";
  if (j.state === "queued") return "排队中";
  if (j.state === "running") return "运行中";
  if (j.state === "succeeded") return "完成";
  if (j.state === "failed") return "失败";
  return p || "—";
}

function stateBadge(st?: string) {
  if (st === "running") return <Badge variant="default">运行中</Badge>;
  if (st === "queued") return <Badge variant="outline">排队</Badge>;
  if (st === "succeeded") return <Badge variant="success">成功</Badge>;
  if (st === "failed") return <Badge variant="warning">失败</Badge>;
  return <Badge variant="outline">{st || "—"}</Badge>;
}

export function ImportsPage() {
  const qc = useQueryClient();
  const [format, setFormat] = useState("sso");
  const [files, setFiles] = useState<File[]>([]);
  const fileRef = useRef<HTMLInputElement>(null);

  const listQ = useQuery({
    queryKey: ["import-jobs"],
    queryFn: () => api<ImportJobsResponse>("/admin/import/jobs"),
  });

  const jobs = listQ.data?.jobs ?? [];
  const limits = listQ.data?.limits;
  const active = useMemo(
    () => jobs.filter((j) => j.state === "queued" || j.state === "running"),
    [jobs],
  );
  const hasSSO = active.some((j) => String(j.format || "").toLowerCase() === "sso");

  useEffect(() => {
    if (!active.length) return;
    const t = window.setTimeout(
      () => void qc.invalidateQueries({ queryKey: ["import-jobs"] }),
      hasSSO ? 800 : 1500,
    );
    return () => window.clearTimeout(t);
  }, [active.length, hasSSO, jobs, qc]);

  const uploadM = useMutation({
    mutationFn: async (fileList: File[]) => {
      const results: { name: string; ok: boolean; error?: string }[] = [];
      for (let i = 0; i < fileList.length; i++) {
        const file = fileList[i];
        const fd = new FormData();
        fd.append("format", format);
        fd.append("file", file, file.name);
        try {
          await api("/admin/import/jobs", { method: "POST", body: fd });
          results.push({ name: file.name, ok: true });
        } catch (e) {
          const msg = e instanceof Error ? e.message : "失败";
          results.push({ name: file.name, ok: false, error: msg });
          if (e instanceof ApiError && e.status === 429) break;
        }
      }
      return results;
    },
    onSuccess: (results) => {
      const ok = results.filter((r) => r.ok).length;
      const fail = results.length - ok;
      if (ok) toast.success(`已创建 ${ok} 个导入任务`);
      if (fail) toast.error(`${fail} 个文件失败`);
      setFiles([]);
      if (fileRef.current) fileRef.current.value = "";
      void qc.invalidateQueries({ queryKey: ["import-jobs"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const accept =
    format === "json"
      ? ".json,application/json"
      : format === "ndjson"
        ? ".ndjson,.jsonl,.txt,text/plain"
        : ".txt,.json,text/plain,application/json";

  return (
    <div className="space-y-4">
      <PageHeader
        title="导入任务"
        description="JSON 秒级落库；SSO 需 Device Flow 换票，下方显示实时进度"
        actions={
          <Button
            variant="ghost"
            size="icon"
            onClick={() => void listQ.refetch()}
            disabled={listQ.isFetching}
            aria-label="刷新"
          >
            <RefreshCw className={listQ.isFetching ? "animate-spin" : undefined} />
          </Button>
        }
      />

      <div className="rounded-lg border border-border bg-card p-4">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1">
            <Label>格式</Label>
            <Select value={format} onValueChange={setFormat}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="sso">SSO</SelectItem>
                <SelectItem value="json">JSON</SelectItem>
                <SelectItem value="ndjson">NDJSON</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1">
            <Label>本地文件（可多选）</Label>
            <input
              ref={fileRef}
              type="file"
              multiple
              accept={accept}
              className="block w-full text-xs file:mr-3 file:rounded-md file:border-0 file:bg-secondary file:px-3 file:py-1.5 file:text-xs"
              onChange={(e) => {
                const list = Array.from(e.target.files ?? []).slice(0, 50);
                setFiles(list);
              }}
            />
          </div>
        </div>
        <p className="mt-2 text-xs text-muted-foreground">
          {limits?.enabled === false
            ? "导入已禁用（设置中开启 import_enabled）"
            : `最多 ${limits?.max_entries ?? 10000} 条`}
          {limits?.max_upload_bytes
            ? ` · 单文件上限 ${formatBytes(limits.max_upload_bytes)}`
            : ""}
          {limits?.sso_converter_configured === false && format === "sso"
            ? " · SSO 转换器未配置（将使用内置 Device Flow）"
            : ""}
          {files.length ? ` · 已选 ${files.length} 个文件` : ""}
        </p>
        {files.length ? (
          <ul className="mt-2 max-h-28 overflow-auto text-[11px] text-muted-foreground">
            {files.map((f) => (
              <li key={f.name + f.size}>
                {f.name} · {formatBytes(f.size)}
              </li>
            ))}
          </ul>
        ) : null}
        <div className="mt-3">
          <Button
            disabled={!files.length || uploadM.isPending || limits?.enabled === false}
            onClick={() => uploadM.mutate(files)}
          >
            <Upload />
            {uploadM.isPending ? "上传中…" : "上传并创建任务"}
          </Button>
        </div>
      </div>

      {active.length > 0 ? (
        <div className="rounded-lg border border-border bg-card p-4">
          <div className="mb-3 text-sm font-medium">进行中</div>
          <div className="space-y-3">
            {active.map((j) => (
              <div key={j.id} className="space-y-1">
                <div className="flex flex-wrap items-center gap-2 text-xs">
                  <span className="mono">{j.id}</span>
                  <span className="text-muted-foreground">
                    {j.format} · {j.source_name || "上传"}
                  </span>
                  {stateBadge(j.state)}
                </div>
                <ProgressBar j={j} />
              </div>
            ))}
          </div>
        </div>
      ) : null}

      <DataTableShell>
        {listQ.isPending ? (
          <LoadingState />
        ) : listQ.isError ? (
          <div className="p-4">
            <ErrorState
              message={
                listQ.error instanceof ApiError ? listQ.error.message : "加载失败"
              }
              onRetry={() => void listQ.refetch()}
            />
          </div>
        ) : !jobs.length ? (
          <EmptyState title="暂无导入任务" />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>来源</TableHead>
                <TableHead>格式</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>进度</TableHead>
                <TableHead>错误</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {jobs.map((j) => (
                <TableRow key={j.id}>
                  <TableCell className="mono">{j.id}</TableCell>
                  <TableCell className="max-w-[160px] truncate">
                    {j.source_name || "—"}
                  </TableCell>
                  <TableCell>{j.format || "—"}</TableCell>
                  <TableCell>{stateBadge(j.state)}</TableCell>
                  <TableCell className="min-w-[180px]">
                    <ProgressBar j={j} />
                  </TableCell>
                  <TableCell className="max-w-[200px] truncate text-destructive">
                    {j.error || "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </DataTableShell>
    </div>
  );
}

function ProgressBar({ j }: { j: ImportJob }) {
  const total = Number(j.total || 0);
  const ok = Number(j.ok || 0);
  const fail = Number(j.fail || 0);
  const done = ok + fail;
  let pct = 0;
  if (total > 0) pct = Math.max(0, Math.min(100, Math.round((done * 100) / total)));
  else if (j.state === "succeeded") pct = 100;

  const text =
    (total > 0 ? `${ok}/${total}` : ok > 0 ? String(ok) : "—") +
    (fail > 0 ? ` · 失败 ${fail}` : "") +
    (total > 0 ? ` · ${pct}%` : "");

  return (
    <div>
      <div className="mb-1 flex justify-between text-[11px] text-muted-foreground">
        <span>{phaseLabel(j)}</span>
        <span className="mono">{text}</span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-muted">
        <div
          className={cn(
            "h-full rounded-full transition-[width]",
            j.state === "failed"
              ? "bg-destructive"
              : j.state === "succeeded"
                ? "bg-success"
                : j.phase === "converting"
                  ? "bg-primary/70"
                  : "bg-primary",
          )}
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  );
}

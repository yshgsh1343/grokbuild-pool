import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, FolderOpen, RefreshCw, Upload } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import { api, ApiError } from "@/shared/api/client";
import type {
  ImportJob,
  ImportJobsResponse,
  ServerDirEntry,
  ServerDirResponse,
} from "@/shared/api/types";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "@/shared/components/data-state";
import { DataTableShell } from "@/shared/components/data-table-shell";
import { PageHeader } from "@/shared/components/page-header";
import { formatBytes } from "@/shared/lib/format";
import { cn } from "@/shared/lib/cn";
import {
  BoolField,
  NumField,
  SettingsSection,
  TextField,
} from "@/shared/settings/settings-fields";
import { useSettingsForm } from "@/shared/settings/use-settings-form";

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
  const [serverPath, setServerPath] = useState("");
  const [serverRoot, setServerRoot] = useState("");
  const [serverEntries, setServerEntries] = useState<ServerDirEntry[]>([]);
  const [serverCurrent, setServerCurrent] = useState("");
  const [serverErr, setServerErr] = useState<string | null>(null);
  const [serverLoading, setServerLoading] = useState(false);
  const [showImportSettings, setShowImportSettings] = useState(false);
  const importSettings = useSettingsForm();

  const listQ = useQuery({
    queryKey: ["import-jobs"],
    queryFn: () => api<ImportJobsResponse>("/admin/import/jobs"),
  });

  const jobs = listQ.data?.jobs ?? [];
  const limits = listQ.data?.limits;
  const serverEnabled = !!(
    limits?.allow_server_path || limits?.import_allow_server_path
  );
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

  const serverImportM = useMutation({
    mutationFn: (path: string) =>
      api("/admin/import/jobs", {
        method: "POST",
        body: { format, path },
      }),
    onSuccess: () => {
      toast.success("已创建服务端导入任务");
      void qc.invalidateQueries({ queryKey: ["import-jobs"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  async function loadServerDir(sub = "") {
    setServerLoading(true);
    setServerErr(null);
    try {
      const q = sub
        ? `/admin/import/server-dir?path=${encodeURIComponent(sub)}`
        : "/admin/import/server-dir";
      const res = await api<ServerDirResponse>(q);
      setServerRoot(res.root || "");
      setServerCurrent(res.path || "");
      setServerEntries(res.entries || []);
    } catch (e) {
      setServerEntries([]);
      setServerErr(e instanceof Error ? e.message : "浏览失败");
    } finally {
      setServerLoading(false);
    }
  }

  const accept =
    format === "json"
      ? ".json,application/json"
      : format === "ndjson"
        ? ".ndjson,.jsonl,.txt,text/plain"
        : ".txt,.json,text/plain,application/json";

  return (
    <div className="space-y-4">
      <PageHeader
        title="导入"
        description="本地上传或服务端路径导入；SSO 需 Device Flow 换票。导入相关参数可在本页展开设置。"
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

      <div className="rounded-lg border border-border bg-card">
        <button
          type="button"
          className="flex w-full items-center gap-2 px-4 py-3 text-left text-sm font-medium"
          onClick={() => setShowImportSettings((v) => !v)}
        >
          {showImportSettings ? (
            <ChevronDown className="size-4 text-muted-foreground" />
          ) : (
            <ChevronRight className="size-4 text-muted-foreground" />
          )}
          导入设置
          <span className="ml-1 text-xs font-normal text-muted-foreground">
            启用 / 条数 / workers / 服务端目录 / SSO
          </span>
        </button>
        {showImportSettings && importSettings.form ? (
          <div className="space-y-3 border-t border-border p-4">
            <SettingsSection
              title="导入限制"
              note="保存后即时热更；进行中任务保持旧值，新任务用新值"
            >
              <BoolField
                form={importSettings.form}
                set={importSettings.set}
                label="启用导入"
                k="import_enabled"
              />
              <NumField
                form={importSettings.form}
                set={importSettings.set}
                label="最大上传字节(0=不限)"
                k="import_max_upload_bytes"
              />
              <NumField
                form={importSettings.form}
                set={importSettings.set}
                label="最大条目/任务"
                k="import_max_entries"
              />
              <NumField
                form={importSettings.form}
                set={importSettings.set}
                label="并发任务数"
                k="import_max_concurrent_jobs"
              />
              <NumField
                form={importSettings.form}
                set={importSettings.set}
                label="解析 workers"
                k="import_workers"
              />
              <NumField
                form={importSettings.form}
                set={importSettings.set}
                label="Canary 热池条数(0=全量)"
                k="import_canary_hot_size"
              />
              <NumField
                form={importSettings.form}
                set={importSettings.set}
                label="Canary 抑制全量重载秒"
                k="import_canary_hold_sec"
              />
              <NumField
                form={importSettings.form}
                set={importSettings.set}
                label="任务超时秒"
                k="import_job_timeout_sec"
              />
              <BoolField
                form={importSettings.form}
                set={importSettings.set}
                label="允许服务端路径"
                k="import_allow_server_path"
              />
              <TextField
                form={importSettings.form}
                set={importSettings.set}
                label="服务端导入根目录"
                k="import_server_dir"
                placeholder="/data/imports 或空=data_dir"
              />
              <TextField
                form={importSettings.form}
                set={importSettings.set}
                label="导入专用代理URL(可选)"
                k="import_proxy_url"
                placeholder="socks5://…"
              />
            </SettingsSection>
            <SettingsSection title="SSO 转换" note="空 endpoint 用内置 Go Device Flow">
              <TextField
                form={importSettings.form}
                set={importSettings.set}
                label="SSO Endpoint"
                k="import_sso_endpoint"
                placeholder="https://…/v1/convert"
              />
              <TextField
                form={importSettings.form}
                set={importSettings.set}
                label="SSO API Key(留空不改)"
                k="import_sso_api_key"
                placeholder={
                  importSettings.meta?.import_sso_api_key_set
                    ? "已配置 · 留空保持"
                    : "未配置"
                }
                type="password"
              />
              <NumField
                form={importSettings.form}
                set={importSettings.set}
                label="SSO max_batch"
                k="import_sso_max_batch"
              />
              <NumField
                form={importSettings.form}
                set={importSettings.set}
                label="SSO timeout 秒"
                k="import_sso_timeout_sec"
              />
              <NumField
                form={importSettings.form}
                set={importSettings.set}
                label="SSO workers"
                k="import_sso_workers"
              />
              <BoolField
                form={importSettings.form}
                set={importSettings.set}
                label="SSO allow_insecure"
                k="import_sso_allow_insecure"
              />
            </SettingsSection>
            <div className="flex justify-end gap-2">
              <Button
                variant="secondary"
                size="sm"
                onClick={importSettings.reload}
                disabled={importSettings.query.isFetching}
              >
                重新加载
              </Button>
              <Button
                size="sm"
                disabled={importSettings.saveMutation.isPending}
                onClick={importSettings.saveFull}
              >
                {importSettings.saveMutation.isPending ? "保存中…" : "保存导入设置"}
              </Button>
            </div>
          </div>
        ) : null}
      </div>

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
            : `最多 ${limits?.max_entries ?? 10000} 条/任务`}
          {limits?.max_upload_bytes
            ? ` · 单文件上限 ${formatBytes(limits.max_upload_bytes)}`
            : ""}
          {limits?.max_concurrent_jobs != null
            ? ` · 并发任务 ${limits.max_concurrent_jobs}`
            : ""}
          {limits?.workers != null ? ` · 解析 workers ${limits.workers}` : ""}
          {limits?.import_sso_workers != null
            ? ` · SSO workers ${limits.import_sso_workers}`
            : ""}
          {serverEnabled
            ? ` · 服务端导入开${
                limits?.import_server_dir
                  ? ` · 根目录 ${limits.import_server_dir}`
                  : " · 根=data_dir"
              }`
            : " · 服务端导入关"}
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

      <div className="rounded-lg border border-border bg-card p-4 space-y-3">
        <div className="text-sm font-medium">服务端路径导入</div>
        <p className="text-xs text-muted-foreground">
          需在设置中开启「允许服务端路径」。可浏览配置根目录下的 .json/.txt/.ndjson，或直接填绝对路径。
        </p>
        <div className="flex flex-wrap items-end gap-2">
          <div className="min-w-[240px] flex-1 space-y-1">
            <Label htmlFor="imp-server-path">服务端路径（文件或目录）</Label>
            <Input
              id="imp-server-path"
              className="mono"
              value={serverPath}
              onChange={(e) => setServerPath(e.target.value)}
              placeholder="需开启 allow_server_path"
              disabled={!serverEnabled}
            />
          </div>
          <Button
            size="sm"
            variant="secondary"
            disabled={!serverEnabled || serverLoading}
            onClick={() => void loadServerDir("")}
          >
            <FolderOpen /> 浏览目录
          </Button>
          <Button
            size="sm"
            disabled={
              !serverEnabled ||
              !serverPath.trim() ||
              serverImportM.isPending ||
              limits?.enabled === false
            }
            onClick={() => {
              const path = serverPath.trim();
              if (!path) {
                toast.warning("请填写服务端文件或目录路径");
                return;
              }
              serverImportM.mutate(path);
            }}
          >
            {serverImportM.isPending ? "提交中…" : "从服务端导入"}
          </Button>
        </div>
        {serverErr ? (
          <div className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive">
            {serverErr}
          </div>
        ) : null}
        {serverLoading ? (
          <p className="text-xs text-muted-foreground">加载目录…</p>
        ) : serverEntries.length > 0 ? (
          <div className="text-xs">
            <div className="mb-1 text-muted-foreground mono">
              当前: {serverCurrent}
              {serverRoot ? ` · 根: ${serverRoot}` : ""}
            </div>
            <ul className="max-h-40 space-y-1 overflow-auto">
              {serverEntries.map((e) => (
                <li key={e.path}>
                  <button
                    type="button"
                    className="rounded-md px-2 py-1 text-left hover:bg-secondary/60 mono"
                    onClick={() => {
                      if (e.is_dir) {
                        let rel = e.path;
                        if (serverRoot && e.path.startsWith(serverRoot)) {
                          rel = e.path.slice(serverRoot.length).replace(/^[/\\]+/, "");
                        }
                        void loadServerDir(rel);
                      } else {
                        setServerPath(e.path);
                      }
                    }}
                  >
                    {e.is_dir ? "📁 " : "📄 "}
                    {e.name}
                    {!e.is_dir && e.size != null ? ` · ${formatBytes(e.size)}` : ""}
                  </button>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
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

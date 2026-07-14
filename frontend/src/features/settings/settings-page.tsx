import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { toast } from "sonner";

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
import { Textarea } from "@/components/ui/textarea";
import { api, ApiError } from "@/shared/api/client";
import type { RuntimeSettings, SettingsSnapshot } from "@/shared/api/types";
import { ErrorState, LoadingState } from "@/shared/components/data-state";
import { PageHeader } from "@/shared/components/page-header";
import { cn } from "@/shared/lib/cn";

type FormState = {
  availability_mode: string;
  selector_strategy: string;
  hot_size: number;
  max_inflight_per_account: number;
  sticky_ttl_sec: number;
  sticky_max: number;
  pow2_k: number;
  selector_max_attempts: number;
  w_priority: number;
  w_inflight: number;
  w_failure: number;
  jitter_amp: number;

  max_attempts: number;
  cooldown_base_sec: number;
  cooldown_cap_sec: number;
  unauthorized_cooldown_sec: number;
  payment_required_cooldown_sec: number;
  unauthorized_quarantine_after: number;
  forbidden_cooldown_sec: number;
  forbidden_quarantine_after: number;
  cooldown_jitter_pct: number;
  cooldown_exp_max: number;
  quarantine_on_payment_required: boolean;
  clear_sticky_on_429: boolean;
  clear_sticky_on_5xx: boolean;

  max_concurrent: number;
  max_body_bytes: number;
  request_timeout_sec: number;
  logging_level: string;

  refresh_workers: number;
  refresh_qps: number;
  refresh_skew_sec: number;

  token_default_remain_quota: number;
  token_default_max_concurrent: number;
  token_default_rpm: number;
  token_default_unlimited: boolean;

  import_enabled: boolean;
  import_max_upload_bytes: number;
  import_max_entries: number;
  import_max_concurrent_jobs: number;
  import_workers: number;
  import_canary_hot_size: number;
  import_canary_hold_sec: number;
  import_max_ndjson_line_bytes: number;
  import_max_sso_value_bytes: number;
  import_job_timeout_sec: number;
  import_staging_stale_after_sec: number;
  import_allow_server_path: boolean;
  import_sso_endpoint: string;
  import_sso_api_key: string;
  import_sso_max_batch: number;
  import_sso_timeout_sec: number;
  import_sso_workers: number;
  import_sso_allow_insecure: boolean;

  anthropic_enabled: boolean;
  anthropic_strip_unknown_betas: boolean;
  anthropic_count_tokens: boolean;
  anthropic_passthrough_prefixes: string;
  anthropic_model_aliases: string;

  listen: string;
  allow_public_listen: boolean;
  data_dir: string;
  db_path: string;
  upstream_base_url: string;
  oauth_refresh_url: string;
  oauth_client_id: string;
  api_key: string;
  admin_key: string;
};

const SECTIONS = [
  { id: "sel", title: "选号 / 热池" },
  { id: "lease", title: "租约 / 冷却" },
  { id: "http", title: "进程 / HTTP" },
  { id: "refresh", title: "Token 刷新" },
  { id: "token", title: "令牌模板" },
  { id: "import", title: "导入 / SSO" },
  { id: "anthropic", title: "Anthropic" },
  { id: "deploy", title: "部署 / 密钥" },
  { id: "json", title: "JSON 快照" },
] as const;

function aliasesToText(map?: Record<string, string>): string {
  if (!map) return "";
  return Object.keys(map)
    .sort()
    .map((k) => `${k} = ${map[k]}`)
    .join("\n");
}

function textToAliases(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split(/\r?\n/)) {
    const t = line.trim();
    if (!t || t.startsWith("#")) continue;
    const m = t.split(/\s*=\s*|\s*:\s*|\s+/);
    if (m.length >= 2) {
      const k = m[0].trim();
      const v = m.slice(1).join(" ").trim();
      if (k && v) out[k] = v;
    }
  }
  return out;
}

function prefixesToText(arr?: string[]): string {
  return (arr || []).join(", ");
}

function textToPrefixes(text: string): string[] {
  return text
    .split(/[,\n]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

function fromSnapshot(s: SettingsSnapshot): FormState {
  return {
    availability_mode: s.availability_mode || "stable",
    selector_strategy: s.selector_strategy || "stable_rr",
    hot_size: s.hot_size ?? 0,
    max_inflight_per_account: s.max_inflight_per_account ?? 0,
    sticky_ttl_sec: s.sticky_ttl_sec ?? 0,
    sticky_max: s.sticky_max ?? 0,
    pow2_k: s.pow2_k ?? 0,
    selector_max_attempts: s.selector_max_attempts ?? 0,
    w_priority: s.w_priority ?? 0,
    w_inflight: s.w_inflight ?? 0,
    w_failure: s.w_failure ?? 0,
    jitter_amp: s.jitter_amp ?? 0,

    max_attempts: s.max_attempts ?? 0,
    cooldown_base_sec: s.cooldown_base_sec ?? 0,
    cooldown_cap_sec: s.cooldown_cap_sec ?? 0,
    unauthorized_cooldown_sec: s.unauthorized_cooldown_sec ?? 0,
    payment_required_cooldown_sec: s.payment_required_cooldown_sec ?? 0,
    unauthorized_quarantine_after: s.unauthorized_quarantine_after ?? 0,
    forbidden_cooldown_sec: s.forbidden_cooldown_sec ?? 0,
    forbidden_quarantine_after: s.forbidden_quarantine_after ?? 0,
    cooldown_jitter_pct: s.cooldown_jitter_pct ?? 0,
    cooldown_exp_max: s.cooldown_exp_max ?? 0,
    quarantine_on_payment_required: !!s.quarantine_on_payment_required,
    clear_sticky_on_429: !!s.clear_sticky_on_429,
    clear_sticky_on_5xx: !!s.clear_sticky_on_5xx,

    max_concurrent: s.max_concurrent ?? 0,
    max_body_bytes: s.max_body_bytes ?? 0,
    request_timeout_sec: s.request_timeout_sec ?? 0,
    logging_level: s.logging_level || "info",

    refresh_workers: s.refresh_workers ?? 0,
    refresh_qps: s.refresh_qps ?? 0,
    refresh_skew_sec: s.refresh_skew_sec ?? 0,

    token_default_remain_quota: s.token_default_remain_quota ?? 0,
    token_default_max_concurrent: s.token_default_max_concurrent ?? 0,
    token_default_rpm: s.token_default_rpm ?? 0,
    token_default_unlimited: !!s.token_default_unlimited,

    import_enabled: !!s.import_enabled,
    import_max_upload_bytes: s.import_max_upload_bytes ?? 0,
    import_max_entries: s.import_max_entries ?? 0,
    import_max_concurrent_jobs: s.import_max_concurrent_jobs ?? 0,
    import_workers: s.import_workers ?? 0,
    import_canary_hot_size: s.import_canary_hot_size ?? 0,
    import_canary_hold_sec: s.import_canary_hold_sec ?? 300,
    import_max_ndjson_line_bytes: s.import_max_ndjson_line_bytes ?? 0,
    import_max_sso_value_bytes: s.import_max_sso_value_bytes ?? 0,
    import_job_timeout_sec: s.import_job_timeout_sec ?? 0,
    import_staging_stale_after_sec: s.import_staging_stale_after_sec ?? 0,
    import_allow_server_path: !!s.import_allow_server_path,
    import_sso_endpoint: s.import_sso_endpoint || "",
    import_sso_api_key: "",
    import_sso_max_batch: s.import_sso_max_batch ?? 0,
    import_sso_timeout_sec: s.import_sso_timeout_sec ?? 0,
    import_sso_workers: s.import_sso_workers ?? 0,
    import_sso_allow_insecure: !!s.import_sso_allow_insecure,

    anthropic_enabled: !!s.anthropic_enabled,
    anthropic_strip_unknown_betas: !!s.anthropic_strip_unknown_betas,
    anthropic_count_tokens: !!s.anthropic_count_tokens,
    anthropic_passthrough_prefixes: prefixesToText(s.anthropic_passthrough_prefixes),
    anthropic_model_aliases: aliasesToText(s.anthropic_model_aliases),

    listen: s.listen || "",
    allow_public_listen: !!s.allow_public_listen,
    data_dir: s.data_dir || "",
    db_path: s.db_path || "",
    upstream_base_url: s.upstream_base_url || "",
    oauth_refresh_url: s.oauth_refresh_url || "",
    oauth_client_id: s.oauth_client_id || "",
    api_key: "",
    admin_key: "",
  };
}

function toBody(f: FormState): RuntimeSettings {
  const body: RuntimeSettings = {
    availability_mode: f.availability_mode,
    selector_strategy: f.selector_strategy,
    hot_size: f.hot_size,
    max_inflight_per_account: f.max_inflight_per_account,
    sticky_ttl_sec: f.sticky_ttl_sec,
    sticky_max: f.sticky_max,
    pow2_k: f.pow2_k,
    selector_max_attempts: f.selector_max_attempts,
    w_priority: f.w_priority,
    w_inflight: f.w_inflight,
    w_failure: f.w_failure,
    jitter_amp: f.jitter_amp,

    max_attempts: f.max_attempts,
    cooldown_base_sec: f.cooldown_base_sec,
    cooldown_cap_sec: f.cooldown_cap_sec,
    unauthorized_cooldown_sec: f.unauthorized_cooldown_sec,
    payment_required_cooldown_sec: f.payment_required_cooldown_sec,
    unauthorized_quarantine_after: f.unauthorized_quarantine_after,
    forbidden_cooldown_sec: f.forbidden_cooldown_sec,
    forbidden_quarantine_after: f.forbidden_quarantine_after,
    cooldown_jitter_pct: f.cooldown_jitter_pct,
    cooldown_exp_max: f.cooldown_exp_max,
    quarantine_on_payment_required: f.quarantine_on_payment_required,
    clear_sticky_on_429: f.clear_sticky_on_429,
    clear_sticky_on_5xx: f.clear_sticky_on_5xx,

    max_concurrent: f.max_concurrent,
    max_body_bytes: f.max_body_bytes,
    request_timeout_sec: f.request_timeout_sec,
    logging_level: f.logging_level,

    refresh_workers: f.refresh_workers,
    refresh_qps: f.refresh_qps,
    refresh_skew_sec: f.refresh_skew_sec,

    token_default_remain_quota: f.token_default_remain_quota,
    token_default_max_concurrent: f.token_default_max_concurrent,
    token_default_rpm: f.token_default_rpm,
    token_default_unlimited: f.token_default_unlimited,

    import_enabled: f.import_enabled,
    import_max_upload_bytes: f.import_max_upload_bytes,
    import_max_entries: f.import_max_entries,
    import_max_concurrent_jobs: f.import_max_concurrent_jobs,
    import_workers: f.import_workers,
    import_canary_hot_size: f.import_canary_hot_size,
    import_canary_hold_sec: f.import_canary_hold_sec,
    import_max_ndjson_line_bytes: f.import_max_ndjson_line_bytes,
    import_max_sso_value_bytes: f.import_max_sso_value_bytes,
    import_job_timeout_sec: f.import_job_timeout_sec,
    import_staging_stale_after_sec: f.import_staging_stale_after_sec,
    import_allow_server_path: f.import_allow_server_path,
    import_sso_endpoint: f.import_sso_endpoint,
    import_sso_max_batch: f.import_sso_max_batch,
    import_sso_timeout_sec: f.import_sso_timeout_sec,
    import_sso_workers: f.import_sso_workers,
    import_sso_allow_insecure: f.import_sso_allow_insecure,

    anthropic_enabled: f.anthropic_enabled,
    anthropic_strip_unknown_betas: f.anthropic_strip_unknown_betas,
    anthropic_count_tokens: f.anthropic_count_tokens,
    anthropic_passthrough_prefixes: textToPrefixes(f.anthropic_passthrough_prefixes),
    anthropic_model_aliases: textToAliases(f.anthropic_model_aliases),

    listen: f.listen,
    allow_public_listen: f.allow_public_listen,
    data_dir: f.data_dir,
    db_path: f.db_path,
    upstream_base_url: f.upstream_base_url,
    oauth_refresh_url: f.oauth_refresh_url,
    oauth_client_id: f.oauth_client_id,
  };
  if (f.import_sso_api_key.trim()) body.import_sso_api_key = f.import_sso_api_key.trim();
  if (f.api_key.trim()) body.api_key = f.api_key.trim();
  if (f.admin_key.trim()) body.admin_key = f.admin_key.trim();
  return body;
}

export function SettingsPage() {
  const qc = useQueryClient();
  const [form, setForm] = useState<FormState | null>(null);
  const [meta, setMeta] = useState<SettingsSnapshot | null>(null);
  const [activeSec, setActiveSec] = useState<string>("sel");
  const [jsonCollapsed, setJsonCollapsed] = useState(false);

  const q = useQuery({
    queryKey: ["settings"],
    queryFn: () => api<SettingsSnapshot>("/admin/settings"),
  });

  useEffect(() => {
    if (q.data) {
      setForm(fromSnapshot(q.data));
      setMeta(q.data);
    }
  }, [q.data]);

  const saveM = useMutation({
    mutationFn: (body: RuntimeSettings) =>
      api<{ ok: boolean; persisted: boolean; settings: SettingsSnapshot }>(
        "/admin/settings",
        { method: "PUT", body },
      ),
    onSuccess: (res) => {
      const s = res.settings || {};
      const merged: SettingsSnapshot = {
        ...s,
        persisted_path: s.persisted_path || meta?.persisted_path,
      };
      setMeta(merged);
      // Keep form values; only clear secrets
      setForm((f) =>
        f
          ? {
              ...f,
              import_sso_api_key: "",
              api_key: "",
              admin_key: "",
            }
          : f,
      );
      if (merged.restart_hint) {
        toast.warning((res.persisted ? "已保存。" : "已应用。") + merged.restart_hint);
      } else {
        toast.success(res.persisted ? "已保存并热更新（无需重启）" : "已热更新（无需重启）");
      }
      void qc.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const jsonPreview = useMemo(() => {
    if (!meta && !form) return "";
    return JSON.stringify(meta ?? form, null, 2);
  }, [meta, form]);

  if (q.isPending && !form) return <LoadingState />;
  if (q.isError && !form) {
    return (
      <ErrorState
        message={q.error instanceof ApiError ? q.error.message : "加载失败"}
        onRetry={() => void q.refetch()}
      />
    );
  }
  if (!form) return <LoadingState />;

  function set<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((f) => (f ? { ...f, [key]: value } : f));
  }

  function numField(
    label: string,
    key: keyof FormState,
    opts?: { step?: string },
  ) {
    return (
      <div className="space-y-1">
        <Label>{label}</Label>
        <Input
          type="number"
          step={opts?.step}
          value={Number(form![key] ?? 0)}
          onChange={(e) => set(key, (Number(e.target.value) || 0) as FormState[typeof key])}
        />
      </div>
    );
  }

  function textField(
    label: string,
    key: keyof FormState,
    placeholder?: string,
    type: "text" | "password" = "text",
  ) {
    return (
      <div className="space-y-1">
        <Label>{label}</Label>
        <Input
          type={type}
          value={String(form![key] ?? "")}
          placeholder={placeholder}
          onChange={(e) => set(key, e.target.value as FormState[typeof key])}
        />
      </div>
    );
  }

  function boolField(label: string, key: keyof FormState) {
    return (
      <div className="flex h-full items-end gap-2 pb-1">
        <Checkbox
          checked={!!form![key]}
          onCheckedChange={(v) => set(key, !!v as FormState[typeof key])}
          id={String(key)}
        />
        <Label htmlFor={String(key)} className="cursor-pointer">
          {label}
        </Label>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="设置"
        description="手动「保存并应用」后写入；多数项即时热更 · 仅 listen / data_dir / db_path 需重启 · 密钥留空表示不修改"
        actions={
          <>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => void q.refetch()}
              disabled={q.isFetching}
            >
              重新加载
            </Button>
            <Button
              size="sm"
              disabled={saveM.isPending}
              onClick={() => saveM.mutate(toBody(form))}
            >
              {saveM.isPending ? "保存中…" : "保存并应用"}
            </Button>
          </>
        }
      />

      <nav className="flex flex-wrap gap-1" aria-label="设置分组">
        {SECTIONS.map((s) => (
          <button
            key={s.id}
            type="button"
            className={cn(
              "rounded-md px-2.5 py-1 text-xs text-muted-foreground hover:bg-secondary/60 hover:text-foreground",
              activeSec === s.id && "bg-secondary/70 text-foreground",
            )}
            onClick={() => {
              setActiveSec(s.id);
              document.getElementById(`set-sec-${s.id}`)?.scrollIntoView({
                behavior: "smooth",
                block: "start",
              });
            }}
          >
            {s.title}
          </button>
        ))}
      </nav>

      {meta?.restart_hint ? (
        <div className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning">
          {meta.restart_hint}
          <div className="mt-1 text-muted-foreground">
            管理台不会自动重启服务；请在维护窗口手动重启容器/进程。
          </div>
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">
          持久化：{meta?.persisted_path || "（内存）"} · 点「保存并应用」才会写入 · 密钥留空=不改
        </p>
      )}

      <Section id="sel" title="选号 / 热池" note="策略/权重/粘性即时生效；热池大小保存后 Resize 并重建热集">
        <div className="space-y-1">
          <Label>可用性模式</Label>
          <Select
            value={form.availability_mode}
            onValueChange={(v) => set("availability_mode", v)}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="stable">stable（求活）</SelectItem>
              <SelectItem value="balanced">balanced</SelectItem>
              <SelectItem value="aggressive">aggressive（冲吞吐）</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1">
          <Label>策略</Label>
          <Select
            value={form.selector_strategy}
            onValueChange={(v) => set("selector_strategy", v)}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="stable_rr">stable_rr（最高优层轮询）</SelectItem>
              <SelectItem value="pow2_least_load">pow2_least_load（吞吐）</SelectItem>
            </SelectContent>
          </Select>
        </div>
        {numField("热池大小", "hot_size")}
        {numField("单账号最大并发", "max_inflight_per_account")}
        {numField("粘性 TTL 秒", "sticky_ttl_sec")}
        {numField("粘性 LRU 容量", "sticky_max")}
        {numField("Pow2 K", "pow2_k")}
        {numField("选号 failover", "selector_max_attempts")}
        {numField("权重·优先级", "w_priority", { step: "0.01" })}
        {numField("权重·inflight", "w_inflight", { step: "0.01" })}
        {numField("权重·失败", "w_failure", { step: "0.01" })}
        {numField("抖动幅度", "jitter_amp", { step: "0.01" })}
      </Section>

      <Section id="lease" title="租约 / 防封号冷却" note="429 指数退避；401/402/403 冷却与隔离">
        {numField("Lease failover 次数", "max_attempts")}
        {numField("429 冷却基数秒", "cooldown_base_sec")}
        {numField("冷却上限秒", "cooldown_cap_sec")}
        {numField("429 指数上限", "cooldown_exp_max")}
        {numField("冷却抖动 %", "cooldown_jitter_pct")}
        {numField("401 冷却秒", "unauthorized_cooldown_sec")}
        {numField("402 冷却秒", "payment_required_cooldown_sec")}
        {numField("401 隔离阈值", "unauthorized_quarantine_after")}
        {numField("403 冷却秒", "forbidden_cooldown_sec")}
        {numField("403 隔离阈值(0=关)", "forbidden_quarantine_after")}
        {boolField("402 是否隔离", "quarantine_on_payment_required")}
        {boolField("429 清粘性", "clear_sticky_on_429")}
        {boolField("5xx 清粘性", "clear_sticky_on_5xx")}
      </Section>

      <Section id="http" title="进程限制 / HTTP" note="全局并发、Body、超时立即生效">
        {numField("全局最大并发", "max_concurrent")}
        {numField("最大 Body 字节", "max_body_bytes")}
        {numField("请求超时秒", "request_timeout_sec")}
        <div className="space-y-1">
          <Label>日志级别</Label>
          <Select
            value={form.logging_level}
            onValueChange={(v) => set("logging_level", v)}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {["debug", "info", "warn", "error"].map((l) => (
                <SelectItem key={l} value={l}>
                  {l}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </Section>

      <Section id="refresh" title="Token 刷新 workers" note="QPS / Skew / Workers 保存后即时生效">
        {numField("Workers（2–4）", "refresh_workers")}
        {numField("Refresh QPS", "refresh_qps", { step: "0.1" })}
        {numField("Skew 秒", "refresh_skew_sec")}
      </Section>

      <Section id="token" title="令牌创建默认模板" note="仅影响管理台创建表单默认值">
        {numField("默认额度", "token_default_remain_quota")}
        {numField("默认并发", "token_default_max_concurrent")}
        {numField("默认 RPM", "token_default_rpm")}
        {boolField("默认无限额度", "token_default_unlimited")}
      </Section>

      <Section id="import" title="导入 / SSO 转换" note="JSON 秒级落库；SSO 需 Device Flow 换票">
        {boolField("启用导入", "import_enabled")}
        {numField("最大上传字节(0=不限)", "import_max_upload_bytes")}
        {numField("最大条目", "import_max_entries")}
        {numField("并发任务数", "import_max_concurrent_jobs")}
        {numField("解析 workers", "import_workers")}
        {numField("导入 Canary 热池条数(0=全量)", "import_canary_hot_size")}
        {numField("Canary 抑制全量重载秒", "import_canary_hold_sec")}
        {numField("NDJSON 行上限", "import_max_ndjson_line_bytes")}
        {numField("SSO 值上限", "import_max_sso_value_bytes")}
        {numField("任务超时秒", "import_job_timeout_sec")}
        {numField("Staging 过期秒", "import_staging_stale_after_sec")}
        {boolField("允许服务端路径", "import_allow_server_path")}
        {textField("SSO Endpoint", "import_sso_endpoint", "https://…/v1/convert")}
        {textField(
          "SSO API Key(留空不改)",
          "import_sso_api_key",
          meta?.import_sso_api_key_set ? "已配置 · 留空保持" : "未配置",
          "password",
        )}
        {numField("SSO max_batch", "import_sso_max_batch")}
        {numField("SSO timeout 秒", "import_sso_timeout_sec")}
        {numField("SSO workers", "import_sso_workers")}
        {boolField("SSO allow_insecure", "import_sso_allow_insecure")}
      </Section>

      <Section id="anthropic" title="Anthropic / 模型别名" note="别名每行：claude-sonnet-4 = grok-4.5">
        {boolField("启用 Anthropic", "anthropic_enabled")}
        {boolField("剥离未知 betas", "anthropic_strip_unknown_betas")}
        {boolField("count_tokens", "anthropic_count_tokens")}
        {textField("透传前缀(逗号分隔)", "anthropic_passthrough_prefixes", "grok-")}
        <div className="space-y-1 sm:col-span-2">
          <Label>模型别名映射</Label>
          <Textarea
            rows={8}
            value={form.anthropic_model_aliases}
            onChange={(e) => set("anthropic_model_aliases", e.target.value)}
            className="mono"
          />
        </div>
      </Section>

      <Section
        id="deploy"
        title="部署 / 上游 / 密钥"
        note="upstream / OAuth 热更；listen / data_dir / db_path 需重启；密钥留空不改"
      >
        {textField("Listen（需重启）", "listen")}
        {boolField("Allow public listen", "allow_public_listen")}
        {textField("Data dir（需重启）", "data_dir")}
        {textField("DB path（需重启）", "db_path")}
        {textField("Upstream base URL", "upstream_base_url", "https://…/v1")}
        {textField("OAuth refresh URL", "oauth_refresh_url")}
        {textField("OAuth client_id", "oauth_client_id")}
        {textField(
          "API Key(留空不改)",
          "api_key",
          meta?.api_key_configured ? "已配置" : "未配置",
          "password",
        )}
        {textField(
          "Admin Key(留空不改)",
          "admin_key",
          meta?.admin_key_configured ? "已配置" : "未配置",
          "password",
        )}
      </Section>

      <section id="set-sec-json" className="rounded-lg border border-border bg-card p-4">
        <div className="mb-2 flex items-center justify-between">
          <div className="text-sm font-medium">JSON 快照</div>
          <Button
            size="sm"
            variant="secondary"
            onClick={() => setJsonCollapsed((v) => !v)}
          >
            {jsonCollapsed ? "展开" : "收起"}
          </Button>
        </div>
        {!jsonCollapsed ? (
          <pre className="mono max-h-96 overflow-auto rounded-md bg-muted p-3 text-[11px] text-muted-foreground">
            {jsonPreview}
          </pre>
        ) : null}
      </section>
    </div>
  );
}

function Section({
  id,
  title,
  note,
  children,
}: {
  id: string;
  title: string;
  note?: string;
  children: ReactNode;
}) {
  return (
    <section
      id={`set-sec-${id}`}
      className="scroll-mt-20 rounded-lg border border-border bg-card p-4"
    >
      <div className="text-sm font-medium">{title}</div>
      {note ? <p className="mt-1 text-xs text-muted-foreground">{note}</p> : null}
      <div className="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">{children}</div>
    </section>
  );
}

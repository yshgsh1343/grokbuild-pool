import { Link } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { ErrorState, LoadingState } from "@/shared/components/data-state";
import { PageHeader } from "@/shared/components/page-header";
import { ApiError } from "@/shared/api/client";
import {
  BoolField,
  NumField,
  SelectField,
  SettingsSection,
  TextField,
  AreaField,
} from "@/shared/settings/settings-fields";
import { useSettingsForm } from "@/shared/settings/use-settings-form";

/**
 * Core settings only. Import / scheduler / proxy / JSON live on their own pages.
 */
export function SettingsPage() {
  const { form, set, meta, query, saveMutation, saveFull, reload } =
    useSettingsForm();

  if (query.isPending && !form) return <LoadingState />;
  if (query.isError && !form) {
    return (
      <ErrorState
        message={query.error instanceof ApiError ? query.error.message : "加载失败"}
        onRetry={() => void query.refetch()}
      />
    );
  }
  if (!form) return <LoadingState />;

  return (
    <div className="space-y-4">
      <PageHeader
        title="设置"
        description="进程、刷新、令牌模板、Anthropic、部署与密钥。选号 / 导入 / 代理 / JSON 已拆到对应导航。"
        actions={
          <>
            <Button variant="secondary" size="sm" onClick={reload} disabled={query.isFetching}>
              重新加载
            </Button>
            <Button size="sm" disabled={saveMutation.isPending} onClick={saveFull}>
              {saveMutation.isPending ? "保存中…" : "保存并应用"}
            </Button>
          </>
        }
      />

      <div className="flex flex-wrap gap-2 text-xs">
        <Link className="rounded-md bg-secondary/70 px-2.5 py-1 text-foreground hover:bg-secondary" to="/scheduler">
          选号模式 →
        </Link>
        <Link className="rounded-md bg-secondary/70 px-2.5 py-1 text-foreground hover:bg-secondary" to="/imports">
          导入设置 →
        </Link>
        <Link className="rounded-md bg-secondary/70 px-2.5 py-1 text-foreground hover:bg-secondary" to="/proxy-pool">
          代理池 →
        </Link>
        <Link className="rounded-md bg-secondary/70 px-2.5 py-1 text-foreground hover:bg-secondary" to="/settings/json">
          JSON 编辑 →
        </Link>
      </div>

      {meta?.restart_hint ? (
        <div className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning">
          {meta.restart_hint}
          <div className="mt-1 text-muted-foreground">
            管理台不会自动重启服务；请在维护窗口手动重启容器/进程。
          </div>
        </div>
      ) : null}

      <SettingsSection id="http" title="进程限制 / HTTP" note="全局并发、Body、超时立即生效">
        <NumField form={form} set={set} label="全局最大并发" k="max_concurrent" />
        <NumField form={form} set={set} label="最大 Body 字节" k="max_body_bytes" />
        <NumField form={form} set={set} label="请求超时秒" k="request_timeout_sec" />
        <SelectField
          form={form}
          set={set}
          label="日志级别"
          k="logging_level"
          options={["debug", "info", "warn", "error"].map((l) => ({
            value: l,
            label: l,
          }))}
        />
      </SettingsSection>

      <SettingsSection
        id="refresh"
        title="Token 刷新"
        note="QPS / Skew / Workers 保存后即时生效（Workers 只增补不杀在途）"
      >
        <NumField form={form} set={set} label="Workers" k="refresh_workers" />
        <NumField form={form} set={set} label="Refresh QPS" k="refresh_qps" step="0.1" />
        <NumField form={form} set={set} label="Skew 秒" k="refresh_skew_sec" />
      </SettingsSection>

      <SettingsSection id="token" title="令牌创建默认模板" note="仅影响管理台创建表单默认值">
        <NumField form={form} set={set} label="默认额度" k="token_default_remain_quota" />
        <NumField form={form} set={set} label="默认并发" k="token_default_max_concurrent" />
        <NumField form={form} set={set} label="默认 RPM" k="token_default_rpm" />
        <BoolField form={form} set={set} label="默认无限额度" k="token_default_unlimited" />
      </SettingsSection>

      <SettingsSection
        id="anthropic"
        title="Anthropic / 模型别名"
        note="别名每行：claude-sonnet-4 = grok-4.5"
      >
        <BoolField form={form} set={set} label="启用 Anthropic" k="anthropic_enabled" />
        <BoolField form={form} set={set} label="剥离未知 betas" k="anthropic_strip_unknown_betas" />
        <BoolField form={form} set={set} label="count_tokens" k="anthropic_count_tokens" />
        <TextField
          form={form}
          set={set}
          label="透传前缀(逗号分隔)"
          k="anthropic_passthrough_prefixes"
          placeholder="grok-"
        />
        <AreaField form={form} set={set} label="模型别名映射" k="anthropic_model_aliases" rows={8} />
      </SettingsSection>

      <SettingsSection
        id="deploy"
        title="部署 / 上游 / 密钥"
        note="upstream / OAuth 热更；listen / data_dir / db_path 需重启；密钥留空不改"
      >
        <TextField form={form} set={set} label="Listen（需重启）" k="listen" />
        <BoolField form={form} set={set} label="Allow public listen" k="allow_public_listen" />
        <TextField form={form} set={set} label="Data dir（需重启）" k="data_dir" />
        <TextField form={form} set={set} label="DB path（需重启）" k="db_path" />
        <TextField
          form={form}
          set={set}
          label="Upstream base URL"
          k="upstream_base_url"
          placeholder="https://…/v1"
        />
        <TextField form={form} set={set} label="OAuth refresh URL" k="oauth_refresh_url" />
        <TextField form={form} set={set} label="OAuth client_id" k="oauth_client_id" />
        <TextField
          form={form}
          set={set}
          label="API Key(留空不改)"
          k="api_key"
          placeholder={meta?.api_key_configured ? "已配置" : "未配置"}
          type="password"
        />
        <TextField
          form={form}
          set={set}
          label="Admin Key(留空不改)"
          k="admin_key"
          placeholder={meta?.admin_key_configured ? "已配置" : "未配置"}
          type="password"
        />
      </SettingsSection>
    </div>
  );
}

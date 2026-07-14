import { Button } from "@/components/ui/button";
import { ErrorState, LoadingState } from "@/shared/components/data-state";
import { PageHeader } from "@/shared/components/page-header";
import { ApiError } from "@/shared/api/client";
import {
  BoolField,
  NumField,
  SelectField,
  SettingsSection,
} from "@/shared/settings/settings-fields";
import { useSettingsForm } from "@/shared/settings/use-settings-form";

const MODE_HELP: Record<string, string> = {
  stable:
    "求活优先：少换号、单号低并发、429 尽量不清粘性。适合长期挂机与防封号。",
  balanced: "均衡：在稳定与吞吐之间折中，failover 略多、并发略高。",
  aggressive:
    "冲吞吐：pow2 选号、更高并发与换号预算，429 可能清粘性。压测可用，防封较差。",
};

const STRATEGY_HELP: Record<string, string> = {
  stable_rr: "在最高优先级合格账号层内轮询，行为更可预期，减少“砸热号”。",
  pow2_least_load:
    "Power-of-two choices：随机抽 K 个再按权重打分，吞吐更好，流量更集中于低负载号。",
};

export function SchedulerPage() {
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

  const modeNote = MODE_HELP[form.availability_mode] || MODE_HELP.stable;
  const stratNote = STRATEGY_HELP[form.selector_strategy] || STRATEGY_HELP.stable_rr;

  return (
    <div className="space-y-4">
      <PageHeader
        title="选号模式"
        description="可用性档位、选号策略、热池与租约冷却。保存后即时热更（热池 Resize 可能要几秒）。"
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

      {meta?.restart_hint ? (
        <div className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning">
          {meta.restart_hint}
        </div>
      ) : null}

      <div className="rounded-lg border border-border bg-card p-4 text-xs leading-relaxed text-muted-foreground">
        <div className="font-medium text-foreground">当前解释</div>
        <p className="mt-2">
          <span className="text-foreground">可用性模式 · {form.availability_mode}</span>
          <br />
          {modeNote}
        </p>
        <p className="mt-2">
          <span className="text-foreground">策略 · {form.selector_strategy}</span>
          <br />
          {stratNote}
        </p>
        <p className="mt-2">
          粘性：同一会话（session / conv id）优先钉同一账号；账号若绑定代理则出口一并固定。
          建议防封默认：stable + stable_rr + 单号并发 1～2 + 429 不清粘性。
        </p>
      </div>

      <SettingsSection
        id="sel"
        title="选号 / 热池"
        note="策略/权重/粘性即时生效；热池大小保存后 Resize 并重建热集"
      >
        <SelectField
          form={form}
          set={set}
          label="可用性模式"
          k="availability_mode"
          options={[
            { value: "stable", label: "stable（求活 / 防封）" },
            { value: "balanced", label: "balanced（均衡）" },
            { value: "aggressive", label: "aggressive（冲吞吐）" },
          ]}
        />
        <SelectField
          form={form}
          set={set}
          label="选号策略"
          k="selector_strategy"
          options={[
            { value: "stable_rr", label: "stable_rr（最高优层轮询）" },
            { value: "pow2_least_load", label: "pow2_least_load（吞吐）" },
          ]}
        />
        <NumField form={form} set={set} label="热池大小" k="hot_size" />
        <NumField form={form} set={set} label="单账号最大并发" k="max_inflight_per_account" />
        <NumField form={form} set={set} label="粘性 TTL 秒" k="sticky_ttl_sec" />
        <NumField form={form} set={set} label="粘性 LRU 容量" k="sticky_max" />
        <NumField form={form} set={set} label="Pow2 K" k="pow2_k" />
        <NumField form={form} set={set} label="选号 failover" k="selector_max_attempts" />
        <NumField form={form} set={set} label="权重·优先级" k="w_priority" step="0.01" />
        <NumField form={form} set={set} label="权重·inflight" k="w_inflight" step="0.01" />
        <NumField form={form} set={set} label="权重·失败" k="w_failure" step="0.01" />
        <NumField form={form} set={set} label="抖动幅度" k="jitter_amp" step="0.01" />
      </SettingsSection>

      <SettingsSection
        id="lease"
        title="租约 / 防封号冷却"
        note="429 指数退避；401/402/403 冷却与隔离。防封建议：429/5xx 默认不清粘性。"
      >
        <NumField form={form} set={set} label="Lease failover 次数" k="max_attempts" />
        <NumField form={form} set={set} label="429 冷却基数秒" k="cooldown_base_sec" />
        <NumField form={form} set={set} label="冷却上限秒" k="cooldown_cap_sec" />
        <NumField form={form} set={set} label="429 指数上限" k="cooldown_exp_max" />
        <NumField form={form} set={set} label="冷却抖动 %" k="cooldown_jitter_pct" />
        <NumField form={form} set={set} label="401 冷却秒" k="unauthorized_cooldown_sec" />
        <NumField form={form} set={set} label="402 冷却秒" k="payment_required_cooldown_sec" />
        <NumField form={form} set={set} label="401 隔离阈值" k="unauthorized_quarantine_after" />
        <NumField form={form} set={set} label="403 冷却秒" k="forbidden_cooldown_sec" />
        <NumField form={form} set={set} label="403 隔离阈值(0=关)" k="forbidden_quarantine_after" />
        <BoolField form={form} set={set} label="402 是否隔离" k="quarantine_on_payment_required" />
        <BoolField form={form} set={set} label="429 清粘性" k="clear_sticky_on_429" />
        <BoolField form={form} set={set} label="5xx 清粘性" k="clear_sticky_on_5xx" />
      </SettingsSection>
    </div>
  );
}

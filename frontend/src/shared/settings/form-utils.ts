import type { RuntimeSettings, SettingsSnapshot } from "@/shared/api/types";

/** Full editable form model (mirrors admin RuntimeSettings + secret write fields). */
export type SettingsFormState = {
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

  require_proxy: boolean;
  proxy_pool_enabled: boolean;
  proxy_assign_mode: string;
  import_proxy_url: string;

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
  import_server_dir: string;
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

export function aliasesToText(map?: Record<string, string>): string {
  if (!map) return "";
  return Object.keys(map)
    .sort()
    .map((k) => `${k} = ${map[k]}`)
    .join("\n");
}

export function textToAliases(text: string): Record<string, string> {
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

export function prefixesToText(arr?: string[]): string {
  return (arr || []).join(", ");
}

export function textToPrefixes(text: string): string[] {
  return text
    .split(/[,\n]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export function fromSnapshot(s: SettingsSnapshot): SettingsFormState {
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

    require_proxy: !!s.require_proxy,
    proxy_pool_enabled: !!s.proxy_pool_enabled,
    proxy_assign_mode: s.proxy_assign_mode || "hash",
    import_proxy_url: s.import_proxy_url || "",

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
    import_server_dir: s.import_server_dir || "",
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

/** Full PUT body for /admin/settings (secrets only if non-empty). */
export function toBody(f: SettingsFormState): RuntimeSettings {
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

    require_proxy: f.require_proxy,
    proxy_pool_enabled: f.proxy_pool_enabled,
    proxy_assign_mode: f.proxy_assign_mode,
    import_proxy_url: f.import_proxy_url,

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
    import_server_dir: f.import_server_dir,
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

/**
 * Merge a partial form patch into the latest server snapshot for PUT.
 * Empty secret fields are omitted so they do not clear configured secrets.
 */
export function mergePatchBody(
  base: SettingsSnapshot,
  patch: Partial<SettingsFormState>,
): RuntimeSettings {
  const full = fromSnapshot(base);
  const merged: SettingsFormState = { ...full, ...patch };
  return toBody(merged);
}

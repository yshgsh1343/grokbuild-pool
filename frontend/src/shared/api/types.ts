/** snake_case DTOs matching Go admin API */

export type PoolStats = {
  version?: string;
  uptime_seconds?: number;
  requests_total?: number;
  errors_total?: number;
  success_rate?: number;
  proxy_reject_total?: number;
  proxy_inflight?: number;
  pool_hot_size?: number;
  pool_cooldown_size?: number;
  process_rss_bytes?: number;
  process_sys_bytes?: number;
  go_goroutines?: number;
  tokens_total?: number;
  tokens_enabled?: number;
  tokens_exhausted?: number;
  listen?: string;
  hot_cap?: number;
  max_concurrent?: number;
  refresh_ok_total?: number;
  refresh_fail_total?: number;
  pool_quarantine_count?: number;
  catalog_count?: number;
  catalog_enabled?: number;
  catalog_active?: number;
  catalog_disabled?: number;
  catalog_cooldown?: number;
  catalog_quarantine?: number;
  accounts_available?: number;
  accounts_total?: number;
  pool_failover_total?: number;
  pool_rate_limit_break_total?: number;
  sticky_hit_rate?: number;
  sticky_primary_hits?: number;
  sticky_secondary_hits?: number;
  sticky_reselects?: number;
};

export type AccountBilling = {
  monthly_used?: number | null;
  monthly_limit?: number | null;
  weekly_usage_percent?: number | null;
  grok_build_percent?: number | null;
  period_end?: string;
  probe_ok?: boolean | null;
  probe_status?: number;
  probe_error?: string;
  probed_at?: number;
  updated_at?: number;
};

export type AccountSummary = {
  id: string;
  email?: string;
  name?: string;
  lifecycle?: string;
  proxy_mode?: string;
  proxy_url?: string;
  priority?: number;
  enabled?: boolean;
  manual_disabled?: boolean;
  expires_at?: number;
  cooldown_until?: number;
  failure_count?: number;
  success_count?: number;
  last_success_at?: number | null;
  revision?: number;
  has_access?: boolean;
  has_refresh?: boolean;
  last_error?: string;
  last_used_at?: number | null;
  alive?: boolean;
  success_rate?: number | null;
  inflight?: number;
  billing?: AccountBilling | null;
  cooldown_remaining_sec?: number;
  status_reason?: string;
};

export type AccountsListResponse = {
  accounts: AccountSummary[];
  next_cursor?: string;
  has_more?: boolean;
  limit?: number;
  offset?: number;
  total?: number;
  filter?: {
    status?: string;
    enabled?: string;
    probe?: string;
    lifecycle?: string;
    q?: string;
    sort?: string;
    order?: string;
  };
  stats?: {
    count?: number;
    enabled?: number;
    active?: number;
    cooldown?: number;
    quarantine?: number;
    disabled?: number;
  };
  page?: {
    alive?: number;
    with_billing?: number;
    inflight_sum?: number;
    count?: number;
  };
};

export type ModelCooldown = {
  account_id?: string;
  model?: string;
  cooldown_until?: number;
  last_error?: string;
  updated_at?: number;
  remaining_sec?: number;
};

export type Token = {
  id: string;
  name?: string;
  key_prefix?: string;
  api_key?: string;
  enabled?: boolean;
  remain_quota?: number;
  unlimited_quota?: boolean;
  max_concurrent?: number;
  rpm?: number;
  used_quota?: number;
  request_count?: number;
  inflight?: number;
  expires_at?: number;
  created_at?: number;
  updated_at?: number;
  last_used_at?: number;
};

export type TokenCreateResult = {
  token: Token;
  api_key?: string;
  plaintext?: string;
};

export type TokensListResponse = {
  tokens: Token[];
};

export type TokensCreateResponse = {
  created?: number;
  tokens?: TokenCreateResult[];
  token?: Token;
  api_key?: string;
  plaintext?: string;
};

export type ImportJob = {
  id: string;
  format?: string;
  source_name?: string;
  state?: string;
  total?: number;
  ok?: number;
  fail?: number;
  skipped?: number;
  phase?: string;
  message?: string;
  error?: string;
  started?: string;
  finished?: string;
};

export type ImportJobsResponse = {
  jobs: ImportJob[];
  limits?: {
    enabled?: boolean;
    max_upload_bytes?: number;
    max_entries?: number;
    sso_converter_configured?: boolean;
    max_concurrent_jobs?: number;
    workers?: number;
    max_ndjson_line_bytes?: number;
    max_sso_value_bytes?: number;
    job_timeout_sec?: number;
    staging_stale_after_sec?: number;
    allow_server_path?: boolean;
    import_allow_server_path?: boolean;
    import_server_dir?: string;
    import_workers?: number;
    import_sso_workers?: number;
    import_sso_max_batch?: number;
    import_canary_hot_size?: number;
    import_canary_hold_sec?: number;
  };
};

export type ServerDirEntry = {
  name: string;
  path: string;
  is_dir?: boolean;
  size?: number;
};

export type ServerDirResponse = {
  root?: string;
  path?: string;
  entries?: ServerDirEntry[];
  note?: string;
};

export type RuntimeSettings = {
  availability_mode?: string;
  selector_strategy?: string;
  hot_size?: number;
  max_inflight_per_account?: number;
  sticky_ttl_sec?: number;
  sticky_max?: number;
  pow2_k?: number;
  w_priority?: number;
  w_inflight?: number;
  w_failure?: number;
  jitter_amp?: number;
  selector_max_attempts?: number;

  max_attempts?: number;
  cooldown_base_sec?: number;
  cooldown_cap_sec?: number;
  unauthorized_cooldown_sec?: number;
  payment_required_cooldown_sec?: number;
  unauthorized_quarantine_after?: number;
  forbidden_cooldown_sec?: number;
  forbidden_quarantine_after?: number;
  cooldown_jitter_pct?: number;
  cooldown_exp_max?: number;
  quarantine_on_payment_required?: boolean;
  clear_sticky_on_429?: boolean;
  clear_sticky_on_5xx?: boolean;

  // proxy pool / antiban egress
  require_proxy?: boolean;
  proxy_pool_enabled?: boolean;
  proxy_assign_mode?: string; // hash | least_accounts
  import_proxy_url?: string;

  max_concurrent?: number;
  max_body_bytes?: number;
  request_timeout_sec?: number;

  refresh_workers?: number;
  refresh_qps?: number;
  refresh_skew_sec?: number;

  token_default_remain_quota?: number;
  token_default_max_concurrent?: number;
  token_default_rpm?: number;
  token_default_unlimited?: boolean;

  import_enabled?: boolean;
  import_max_upload_bytes?: number;
  import_max_entries?: number;
  import_max_concurrent_jobs?: number;
  import_workers?: number;
  import_max_ndjson_line_bytes?: number;
  import_max_sso_value_bytes?: number;
  import_job_timeout_sec?: number;
  import_staging_stale_after_sec?: number;
  import_allow_server_path?: boolean;
  import_server_dir?: string;
  import_sso_endpoint?: string;
  import_sso_api_key_set?: boolean;
  import_sso_api_key?: string;
  import_sso_max_batch?: number;
  import_sso_timeout_sec?: number;
  import_sso_allow_insecure?: boolean;
  import_sso_workers?: number;
  import_canary_hot_size?: number;
  import_canary_hold_sec?: number;

  anthropic_enabled?: boolean;
  anthropic_strip_unknown_betas?: boolean;
  anthropic_count_tokens?: boolean;
  anthropic_passthrough_prefixes?: string[];
  anthropic_model_aliases?: Record<string, string>;

  listen?: string;
  allow_public_listen?: boolean;
  data_dir?: string;
  db_path?: string;
  upstream_base_url?: string;
  oauth_refresh_url?: string;
  oauth_client_id?: string;
  api_key_configured?: boolean;
  admin_key_configured?: boolean;
  api_key?: string;
  admin_key?: string;
  logging_level?: string;
  restart_hint?: string;
};

export type SettingsSnapshot = RuntimeSettings & {
  persisted_path?: string;
};

export type BatchResult = {
  action?: string;
  ok?: number;
  failed?: number;
  deleted?: number;
  ids_ok?: string[];
  errors?: { id?: string; error?: string }[];
};

export type ProxyNode = {
  id?: string;
  url: string;
  enabled?: boolean;
  weight?: number;
  fail_count?: number;
  cooldown_until?: number;
  last_error?: string;
  assigned_accounts?: number;
};

export type ProxyPoolResponse = {
  path?: string;
  enabled?: boolean;
  require_proxy?: boolean;
  assign_mode?: string;
  healthy?: number;
  nodes?: ProxyNode[];
  note?: string;
};

export type ProxyPoolAssignResponse = {
  ok?: boolean;
  assigned?: number;
  skipped?: number;
  failed?: number;
  dry_run?: boolean;
  mode?: string;
  healthy?: number;
};

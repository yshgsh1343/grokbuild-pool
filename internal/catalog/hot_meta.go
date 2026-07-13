package catalog

// HotMetaFromAccount 从冷存储账号构造热池元数据（无密钥）。
func HotMetaFromAccount(a Account) HotMeta {
	return HotMeta{
		ID:            a.ID,
		Priority:      int32(a.Priority),
		CooldownUntil: a.CooldownUntil,
		ExpiresAt:     a.ExpiresAt,
		Inflight:      0,
		FailureScore:  float32(a.FailureCount),
		Enabled:       a.Enabled && !a.ManualDisabled,
		Lifecycle:     a.Lifecycle,
		Revision:      a.Revision,
		IdentityKey:   a.IdentityKey,
		ProxyMode:     a.ProxyMode,
		ProxyURL:      a.ProxyURL,
	}
}

// ProbeFailedInBilling 报告 billing_json 是否明确记录 probe_ok=false。
func ProbeFailedInBilling(billingJSON string) bool {
	v := ParseAccountBillingView(billingJSON)
	return v != nil && v.ProbeOK != nil && !*v.ProbeOK
}

// ProbeOKInBilling 报告 billing_json 是否明确记录 probe_ok=true。
func ProbeOKInBilling(billingJSON string) bool {
	v := ParseAccountBillingView(billingJSON)
	return v != nil && v.ProbeOK != nil && *v.ProbeOK
}

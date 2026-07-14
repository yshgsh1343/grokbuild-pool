// Package lease 在 catalog + hot + selector 之上提供 Acquire/Release 协调。
//
// Lease 仅在一次上游调用期间携带账号密钥。
// 令牌绝不可记日志；请用 Lease.String() / 脱敏辅助。
//
// 防封号 P1（与 sticky 协同）：
//   - Acquire 在 sticky 命中时固定 AccountID，并从 catalog 填入 ProxyURL/ProxyMode；
//   - executor.UpstreamFor 使用 lease.ProxyURL（及 ClientFor 账号亲和）出站；
//   - 因此「会话粘性 = 账号粘性 = 代理粘性」，同一 stickyKey 会话不会在账号间漂移代理。
//
// 详见 phases/ANTIBAN_P1.md。
//
// Scheme B 租约选号模块。
package lease

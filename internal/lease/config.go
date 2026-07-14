package lease

// 默认配置：可用性优先（stable），对齐“额度管家”而非极限故障切换。
const (
	DefaultMaxAttempts                 = 2
	DefaultCooldownBaseSec             = 30
	DefaultCooldownCapSec              = 300
	DefaultUnauthorizedCooldownSec       = 60
	DefaultPaymentRequiredCooldownSec     = 180
	DefaultUnauthorizedQuarantineAfter = 5
	// DefaultForbiddenCooldownSec 403 冷却（秒），默认 5 分钟。
	DefaultForbiddenCooldownSec = 300
	// DefaultCooldownJitterPct 冷却抖动百分比（0-50），默认 20。
	DefaultCooldownJitterPct = 20
	// DefaultCooldownExpMax 429 指数退避最大位移。
	DefaultCooldownExpMax = 3
	// DefaultMaxInflightPerAccount 与 hot 对齐的文档默认。
	DefaultMaxInflightPerAccount = 2
)

// Config 控制获取失败切换与释放时的冷却策略。
type Config struct {
	// MaxAttempts 为 Acquire 失败切换预算（默认 2）。
	MaxAttempts int

	// CooldownBaseSec：429 且 Result.RetryAfter 为 0 时使用（默认 30）。
	CooldownBaseSec int64

	// CooldownCapSec 限制任意计算出的冷却上限（默认 300）。
	CooldownCapSec int64

	// UnauthorizedCooldownSec 用于 401（默认 60）。
	UnauthorizedCooldownSec int64

	// PaymentRequiredCooldownSec 用于 402（默认 180）。
	PaymentRequiredCooldownSec int64

	// UnauthorizedQuarantineAfter 次连续 401 后隔离账号（默认 5）。
	UnauthorizedQuarantineAfter int

	// ForbiddenCooldownSec 403 专用冷却（默认 300）。
	ForbiddenCooldownSec int64

	// ForbiddenQuarantineAfter 次连续 403 后隔离账号；0=关闭（默认）。
	ForbiddenQuarantineAfter int

	// QuarantineOnPaymentRequired 402 是否隔离；默认 false，仅冷却。
	QuarantineOnPaymentRequired bool

	// ClearStickyOn429 429 时是否立即清粘性；默认 false（连续伤害由换号预算限制）。
	ClearStickyOn429 bool

	// ClearStickyOn5xx 网络/5xx 是否清粘性；默认 false。
	ClearStickyOn5xx bool

	// CooldownJitterPct 为冷却时长附加的随机抖动百分比（默认 20，上限 50；0=关闭）。
	CooldownJitterPct int

	// CooldownExpMax 429 指数退避最大位移（base * 2^min(fail,ExpMax)），默认 3。
	CooldownExpMax int
}

// DefaultConfig 返回可用性优先默认值。
func DefaultConfig() Config {
	return Config{
		MaxAttempts:                 DefaultMaxAttempts,
		CooldownBaseSec:             DefaultCooldownBaseSec,
		CooldownCapSec:              DefaultCooldownCapSec,
		UnauthorizedCooldownSec:    DefaultUnauthorizedCooldownSec,
		PaymentRequiredCooldownSec:  DefaultPaymentRequiredCooldownSec,
		UnauthorizedQuarantineAfter: DefaultUnauthorizedQuarantineAfter,
		ForbiddenCooldownSec:        DefaultForbiddenCooldownSec,
		ForbiddenQuarantineAfter:    0, // 默认关闭
		QuarantineOnPaymentRequired: false,
		ClearStickyOn429:            false,
		ClearStickyOn5xx:            false,
		CooldownJitterPct:           DefaultCooldownJitterPct,
		CooldownExpMax:              DefaultCooldownExpMax,
	}
}

// normalize 将零/负字段填为默认值。
func (c Config) normalize() Config {
	d := DefaultConfig()
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = d.MaxAttempts
	}
	if c.CooldownBaseSec <= 0 {
		c.CooldownBaseSec = d.CooldownBaseSec
	}
	if c.CooldownCapSec <= 0 {
		c.CooldownCapSec = d.CooldownCapSec
	}
	if c.UnauthorizedCooldownSec <= 0 {
		c.UnauthorizedCooldownSec = d.UnauthorizedCooldownSec
	}
	if c.PaymentRequiredCooldownSec <= 0 {
		c.PaymentRequiredCooldownSec = d.PaymentRequiredCooldownSec
	}
	if c.UnauthorizedQuarantineAfter <= 0 {
		c.UnauthorizedQuarantineAfter = d.UnauthorizedQuarantineAfter
	}
	if c.ForbiddenCooldownSec <= 0 {
		c.ForbiddenCooldownSec = d.ForbiddenCooldownSec
	}
	// ForbiddenQuarantineAfter：0 合法（关闭），负值归零
	if c.ForbiddenQuarantineAfter < 0 {
		c.ForbiddenQuarantineAfter = 0
	}
	if c.CooldownJitterPct < 0 {
		c.CooldownJitterPct = d.CooldownJitterPct
	}
	if c.CooldownJitterPct > 100 {
		c.CooldownJitterPct = 100
	}
	if c.CooldownExpMax <= 0 {
		c.CooldownExpMax = d.CooldownExpMax
	}
	return c
}

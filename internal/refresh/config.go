package refresh

import "time"

// Scheme B 默认值（AGENT_PLAN 第 4 节）。
const (
	DefaultWorkers            = 3
	DefaultQPS                = 30.0
	DefaultSkewSec            = 300 // expires_at < now+5m 时刷新
	DefaultScanInterval       = 5 * time.Second
	DefaultScanLimit          = 200
	DefaultJobBuffer          = 512
	DefaultCooldownOnFailSec  = 60
	DefaultCooldownCapSec     = 900
	DefaultQuarantineAfter    = 5 // 连续刷新失败次数
	MinWorkers                = 1
	MaxWorkers                = 256
)

// Config 控制后台刷新 worker 与 EnsureFresh 行为。
type Config struct {
	// Workers 为后台池大小（1–256；默认 3）。
	Workers int

	// QPS 为全局刷新速率上限（默认 30/s）。
	QPS float64

	// Burst 为限流器突发量（默认 max(1, int(QPS/10))）。
	Burst int

	// SkewSec：expires_at < now+SkewSec 时选中/确保刷新（默认 300）。
	SkewSec int64

	// ScanInterval 为 ListExpiring 扫描间隔（默认 5s）。
	ScanInterval time.Duration

	// ScanLimit 为每次扫描最多拉取账号数（默认 200）。
	ScanLimit int

	// JobBuffer 为 worker 任务通道容量（默认 512）。
	JobBuffer int

	// CooldownOnFailSec 刷新失败时的冷却秒数（默认 60）。
	CooldownOnFailSec int64

	// CooldownCapSec 限制失败冷却上限（默认 900）。
	CooldownCapSec int64

	// QuarantineAfter 次连续刷新失败 → lifecycle 隔离（默认 5）。
	QuarantineAfter int
}

// DefaultConfig 返回 Scheme B 刷新默认值。
func DefaultConfig() Config {
	return Config{
		Workers:          DefaultWorkers,
		QPS:              DefaultQPS,
		Burst:            0, // 在 normalize 中填充
		SkewSec:          DefaultSkewSec,
		ScanInterval:     DefaultScanInterval,
		ScanLimit:        DefaultScanLimit,
		JobBuffer:        DefaultJobBuffer,
		CooldownOnFailSec: DefaultCooldownOnFailSec,
		CooldownCapSec:    DefaultCooldownCapSec,
		QuarantineAfter:  DefaultQuarantineAfter,
	}
}

func (c Config) normalize() Config {
	d := DefaultConfig()
	if c.Workers <= 0 {
		c.Workers = d.Workers
	}
	if c.Workers < MinWorkers {
		c.Workers = MinWorkers
	}
	if c.Workers > MaxWorkers {
		c.Workers = MaxWorkers
	}
	if c.QPS <= 0 {
		c.QPS = d.QPS
	}
	if c.Burst <= 0 {
		b := int(c.QPS / 10)
		if b < 1 {
			b = 1
		}
		if b > 10 {
			b = 10
		}
		c.Burst = b
	}
	if c.SkewSec <= 0 {
		c.SkewSec = d.SkewSec
	}
	if c.ScanInterval <= 0 {
		c.ScanInterval = d.ScanInterval
	}
	if c.ScanLimit <= 0 {
		c.ScanLimit = d.ScanLimit
	}
	if c.JobBuffer <= 0 {
		c.JobBuffer = d.JobBuffer
	}
	if c.CooldownOnFailSec <= 0 {
		c.CooldownOnFailSec = d.CooldownOnFailSec
	}
	if c.CooldownCapSec <= 0 {
		c.CooldownCapSec = d.CooldownCapSec
	}
	if c.QuarantineAfter <= 0 {
		c.QuarantineAfter = d.QuarantineAfter
	}
	return c
}

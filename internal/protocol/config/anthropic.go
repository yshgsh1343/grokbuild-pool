// Package config holds the slim Anthropic-facing settings needed by protocol handlers.
// Full pool-proxy config lives under internal/config (M10); this package is a copy surface
// so anthropic handlers do not depend on the root proxy config tree.
package config

import "strings"

// AnthropicConfig controls Claude Code / Anthropic Messages entry behavior.
type AnthropicConfig struct {
	Enabled             bool              `yaml:"enabled"`
	ModelAliases        map[string]string `yaml:"model_aliases"`
	PassthroughPrefixes []string          `yaml:"passthrough_prefixes"`
	StripUnknownBetas   bool              `yaml:"strip_unknown_betas"`
	CountTokens         bool              `yaml:"count_tokens"`
}

// Config is a minimal root config used by ported anthropic tests (Default().Anthropic).
type Config struct {
	Anthropic AnthropicConfig `yaml:"anthropic"`
}

// Default returns protocol defaults matching grokbuild-proxy Anthropic aliases.
func Default() Config {
	return Config{
		Anthropic: AnthropicConfig{
			Enabled: true,
			// 精简可发现别名：列表更短；请求侧仍支持这些 id，且 passthrough grok-*。
			// 旧版号（如 claude-opus-4-8）不在列表中，但可通过请求透传/兼容解析（见 ResolveModel 注释）。
			ModelAliases: map[string]string{
				"claude-sonnet-4":   "grok-4.5",
				"claude-sonnet-4-6": "grok-4.5",
				"claude-opus-4":     "grok-4.5",
				"claude-opus-4-6":   "grok-4.5",
				"claude-haiku-4":    "grok-4.5",
				"claude-haiku-4-5":  "grok-4.5",
				"sonnet":            "grok-4.5",
				"opus":              "grok-4.5",
				"haiku":             "grok-4.5",
			},
			PassthroughPrefixes: []string{"grok-"},
			StripUnknownBetas:   true,
			CountTokens:         false,
		},
	}
}

// ResolveModel maps an Anthropic model id using explicit aliases, then a small
// compatibility fallback for common Claude Code versioned ids that we intentionally
// omit from the public model list (to keep /v1/models short).
func (c AnthropicConfig) ResolveModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return model
	}
	for _, p := range c.PassthroughPrefixes {
		if p != "" && len(model) >= len(p) && model[:len(p)] == p {
			return model
		}
	}
	if alias, ok := c.ModelAliases[model]; ok && alias != "" {
		return alias
	}
	// 请求兼容：旧/细分版号不出现在 /v1/models，但仍可调用。
	switch {
	case strings.HasPrefix(model, "claude-haiku-"):
		return "grok-4.5"
	case strings.HasPrefix(model, "claude-sonnet-"), strings.HasPrefix(model, "claude-opus-"):
		return "grok-4.5"
	case model == "sonnet" || model == "opus":
		return "grok-4.5"
	case model == "haiku":
		return "grok-4.5"
	}
	return model
}

// ResolveModel on root Config delegates to Anthropic.
func (c Config) ResolveModel(model string) string {
	return c.Anthropic.ResolveModel(model)
}

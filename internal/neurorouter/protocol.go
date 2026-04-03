package neurorouter

import (
	"fmt"
	"sort"
	"strings"
)

// ProtocolMode selects the public wire surface exposed by one proxy instance.
type ProtocolMode string

const (
	ProtocolModeAuto      ProtocolMode = "auto"
	ProtocolModeOpenAI    ProtocolMode = "openai"
	ProtocolModeAnthropic ProtocolMode = "anthropic"
)

// ParseProtocolMode normalizes CLI/config protocol mode values.
func ParseProtocolMode(raw string) (ProtocolMode, error) {
	mode := ProtocolMode(strings.ToLower(strings.TrimSpace(raw)))
	if mode == "" {
		return ProtocolModeAuto, nil
	}

	switch mode {
	case ProtocolModeAuto, ProtocolModeOpenAI, ProtocolModeAnthropic:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported protocol %q (expected auto, openai, or anthropic)", raw)
	}
}

// ResolveProtocolMode determines the single public protocol surface allowed for this proxy config.
func ResolveProtocolMode(cfg ProxyConfig) (ProtocolMode, error) {
	modes := make(map[ProtocolMode]struct{})
	addMode := func(mode ProtocolMode) {
		if mode == "" || mode == ProtocolModeAuto {
			return
		}
		modes[mode] = struct{}{}
	}

	for model, target := range cfg.Targets {
		addMode(protocolModeForTarget(model, target, cfg.Capabilities))
	}
	for model, pool := range cfg.TargetPool {
		for _, poolTarget := range pool {
			addMode(protocolModeForTarget(model, poolTarget.Target, cfg.Capabilities))
		}
	}

	if len(modes) == 0 {
		if cfg.ProtocolMode != "" && cfg.ProtocolMode != ProtocolModeAuto {
			return cfg.ProtocolMode, nil
		}
		return ProtocolModeOpenAI, nil
	}
	if cfg.ProtocolMode != "" && cfg.ProtocolMode != ProtocolModeAuto {
		knownModes := make(map[ProtocolMode]struct{})
		for _, target := range cfg.Targets {
			if mode, ok := knownProtocolModeForBaseURL(target.BaseURL); ok {
				knownModes[mode] = struct{}{}
			}
		}
		for _, pool := range cfg.TargetPool {
			for _, poolTarget := range pool {
				if mode, ok := knownProtocolModeForBaseURL(poolTarget.BaseURL); ok {
					knownModes[mode] = struct{}{}
				}
			}
		}

		if len(knownModes) > 1 {
			names := make([]string, 0, len(knownModes))
			for mode := range knownModes {
				names = append(names, string(mode))
			}
			sort.Strings(names)
			return "", fmt.Errorf(
				"explicit protocol %q conflicts with configured target mix (%s); run separate instances or use neurorouter-pro",
				cfg.ProtocolMode,
				strings.Join(names, ", "),
			)
		}
		for mode := range knownModes {
			if mode != cfg.ProtocolMode {
				return "", fmt.Errorf(
					"explicit protocol %q conflicts with configured target protocol %q",
					cfg.ProtocolMode,
					mode,
				)
			}
		}
		return cfg.ProtocolMode, nil
	}
	if len(modes) == 1 {
		for mode := range modes {
			return mode, nil
		}
	}

	names := make([]string, 0, len(modes))
	for mode := range modes {
		names = append(names, string(mode))
	}
	sort.Strings(names)
	return "", fmt.Errorf(
		"mixed protocol targets (%s) are not supported in the community edition; run separate instances or use neurorouter-pro",
		strings.Join(names, ", "),
	)
}

func protocolModeForTarget(model string, target Target, caps map[string]TargetCapabilities) ProtocolMode {
	if caps != nil {
		if cap, ok := caps[model]; ok {
			if mode := protocolModeForProvider(cap.Provider); mode != ProtocolModeAuto {
				return mode
			}
		}
		if cap, ok := caps["default"]; ok {
			if mode := protocolModeForProvider(cap.Provider); mode != ProtocolModeAuto {
				return mode
			}
		}
	}

	if mode := protocolModeForProvider(detectProviderName(target.BaseURL)); mode != ProtocolModeAuto {
		return mode
	}

	return ProtocolModeOpenAI
}

func protocolModeForProvider(provider string) ProtocolMode {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic", "claude":
		return ProtocolModeAnthropic
	case "openai", "openai-compatible", "groq", "deepseek":
		return ProtocolModeOpenAI
	default:
		return ProtocolModeAuto
	}
}

func knownProtocolModeForBaseURL(baseURL string) (ProtocolMode, bool) {
	switch detectProviderName(baseURL) {
	case "anthropic":
		return ProtocolModeAnthropic, true
	case "openai", "groq", "deepseek":
		return ProtocolModeOpenAI, true
	default:
		return ProtocolModeAuto, false
	}
}

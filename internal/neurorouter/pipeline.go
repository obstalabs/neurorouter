package neurorouter

import "errors"

// ErrSecretsDetected is returned when the protection policy is "block" and secrets are found.
var ErrSecretsDetected = errors.New("neurorouter: secrets detected in request")

// PipelineConfig holds the combined filter + protect + neurocache configuration.
type PipelineConfig struct {
	Filters    FilterConfig
	Protection ProtectConfig
	Neurocache NeurocacheConfig
}

// PipelineResult holds metrics from a pipeline run.
type PipelineResult struct {
	SecretsFound      int
	SecretDiagnostics []DetectedSecret
	SecretPolicy      SecretPolicy
	BytesBefore       int
	BytesAfter        int
	FiltersRun        []string
	Blocked           bool
}

// Pipeline orchestrates protect → purify on a request's messages.
type Pipeline struct {
	scanner    *Scanner
	filters    FilterConfig
	policy     SecretPolicy
	neurocache *Neurocache
}

// NewPipeline creates a pipeline from config. Returns nil if everything is disabled.
func NewPipeline(cfg PipelineConfig) *Pipeline {
	var scanner *Scanner
	if cfg.Protection.Enabled {
		scanner = NewScanner()
	}

	var nc *Neurocache
	if cfg.Neurocache.Enabled {
		nc = NewNeurocache()
	}

	if scanner == nil && !hasEnabledFilters(cfg.Filters) && nc == nil {
		return nil
	}

	policy := cfg.Protection.Policy
	if policy == "" {
		policy = PolicyWarn
	}

	return &Pipeline{
		scanner:    scanner,
		filters:    cfg.Filters,
		policy:     policy,
		neurocache: nc,
	}
}

// Process runs the pipeline on messages using the selected per-request adapter.
// Returns the (possibly modified) messages, metrics, and an error if blocked.
func (p *Pipeline) Process(msgs []ChatMessage, adapter FilterAdapter) ([]ChatMessage, *PipelineResult, error) {
	result := &PipelineResult{
		SecretPolicy: p.policy,
		BytesBefore:  messageBytes(msgs),
	}

	// Phase 1: Protect — scan for secrets.
	if p.scanner != nil {
		scan := p.scanner.ScanMessages(msgs)
		result.SecretsFound = len(scan.Secrets)
		result.SecretDiagnostics = cloneDetectedSecrets(scan.Secrets)

		if scan.HasSecrets {
			switch p.policy {
			case PolicyBlock:
				result.Blocked = true
				result.BytesAfter = result.BytesBefore
				return nil, result, ErrSecretsDetected
			case PolicyRedact:
				msgs, _ = p.scanner.RedactMessages(msgs)
			case PolicyWarn:
				// Continue with original messages; caller checks SecretsFound.
			}
		}
	}

	// Phase 2: Purify — run filter chain.
	if adapter == nil {
		adapter = GenericAdapter{}
	}
	if chain := adapter.Filters(p.filters); chain != nil {
		var filterResult *FilterResult
		msgs, filterResult = chain.Run(msgs)
		result.FiltersRun = filterResult.Applied
	}

	result.BytesAfter = messageBytes(msgs)

	// Record patterns for neurocaching.
	if p.neurocache != nil {
		p.neurocache.Record(msgs, result)
	}

	return msgs, result, nil
}

// Suggestions returns accumulated neurocache suggestions. Returns nil if neurocache is disabled.
func (p *Pipeline) Suggestions() []Suggestion {
	if p.neurocache == nil {
		return nil
	}
	return p.neurocache.Suggestions()
}

func hasEnabledFilters(cfg FilterConfig) bool {
	return cfg.StaleReads ||
		cfg.Thinking ||
		cfg.OrphanedResults ||
		cfg.FailedRetries ||
		cfg.SystemReminders ||
		cfg.OversizedBlocks ||
		cfg.StructuredShellMaxBytes > 0
}

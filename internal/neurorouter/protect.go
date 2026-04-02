package neurorouter

import (
	"math"
	"regexp"
	"strings"
)

// SecretType identifies the kind of secret detected.
type SecretType string

const (
	SecretAWSKey       SecretType = "aws_key"
	SecretGitHubToken  SecretType = "github_token"
	SecretStripeKey    SecretType = "stripe_key"
	SecretJWT          SecretType = "jwt"
	SecretDBConn       SecretType = "db_connection"
	SecretSSHKey       SecretType = "ssh_private_key"
	SecretCredential   SecretType = "credential"
	SecretOpenAIKey    SecretType = "openai_key"
	SecretAnthropicKey SecretType = "anthropic_key"
	SecretSlackWebhook SecretType = "slack_webhook"
	SecretDiscordHook  SecretType = "discord_webhook"
	SecretGenericAPI   SecretType = "generic_api_key"
	SecretGroqKey      SecretType = "groq_key"
	SecretNPMToken     SecretType = "npm_token"
	SecretGitLabToken  SecretType = "gitlab_token"
	SecretHighEntropy  SecretType = "high_entropy"
)

// DetectedSecret is a single secret found in message content.
type DetectedSecret struct {
	Type      SecretType `json:"type"`
	Value     string     `json:"preview"` // truncated preview by default
	FullValue string     `json:"-"`       // dangerous local-debug value, never serialized by default
	Line      int        `json:"line"`    // 1-based line number
}

// ProtectResult summarizes a scan of messages.
type ProtectResult struct {
	Secrets    []DetectedSecret
	HasSecrets bool
}

// SecretPolicy determines what happens when secrets are found.
type SecretPolicy string

const (
	PolicyBlock  SecretPolicy = "block"
	PolicyRedact SecretPolicy = "redact"
	PolicyWarn   SecretPolicy = "warn"
)

// ProtectConfig configures secret detection.
type ProtectConfig struct {
	Enabled                       bool
	Policy                        SecretPolicy // default: "warn"
	DangerouslyCaptureFullSecrets bool
}

type secretRule struct {
	Type    SecretType
	Pattern *regexp.Regexp
}

// Scanner holds compiled detection rules.
type Scanner struct {
	rules             []secretRule
	captureFullValues bool
}

// NewScanner creates a scanner with all detection rules compiled.
func NewScanner() *Scanner {
	return NewScannerWithCapture(false)
}

// NewScannerWithCapture creates a scanner and optionally keeps full matched values
// in memory for dangerous local debugging flows.
func NewScannerWithCapture(captureFullValues bool) *Scanner {
	return &Scanner{
		rules:             defaultRules,
		captureFullValues: captureFullValues,
	}
}

var defaultRules = func() []secretRule {
	rules := []struct {
		t SecretType
		p string
	}{
		// SSH private keys.
		{SecretSSHKey, `-----BEGIN\s+(RSA |DSA |EC |OPENSSH )?PRIVATE KEY-----`},
		// AWS access key IDs.
		{SecretAWSKey, `\b(AKIA|ABIA|ACCA|ASIA)[0-9A-Z]{16}\b`},
		// JWT tokens.
		{SecretJWT, `\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`},
		// Database connection strings.
		{SecretDBConn, `(postgres|postgresql|mysql|mongodb|redis)://[^\s"']+`},
		// OpenAI keys.
		{SecretOpenAIKey, `sk-proj-[A-Za-z0-9_-]{20,}`},
		{SecretOpenAIKey, `sk-svcacct-[A-Za-z0-9_-]{20,}`},
		// Anthropic keys.
		{SecretAnthropicKey, `sk-ant-[A-Za-z0-9_-]{20,}`},
		// GitHub tokens.
		{SecretGitHubToken, `ghp_[0-9a-zA-Z]{36}`},
		{SecretGitHubToken, `gho_[0-9a-zA-Z]{36}`},
		{SecretGitHubToken, `ghs_[0-9a-zA-Z]{36}`},
		// Stripe keys.
		{SecretStripeKey, `sk_live_[0-9a-zA-Z]{24,}`},
		{SecretStripeKey, `rk_live_[0-9a-zA-Z]{24,}`},
		// Groq keys.
		{SecretGroqKey, `gsk_[0-9a-zA-Z]{20,}`},
		// npm tokens.
		{SecretNPMToken, `npm_[0-9a-zA-Z]{36,}`},
		// GitLab tokens.
		{SecretGitLabToken, `glpat-[0-9a-zA-Z_-]{20,}`},
		// Slack webhooks.
		{SecretSlackWebhook, `https://hooks\.slack\.com/services/T[A-Z0-9]+/B[A-Z0-9]+/[a-zA-Z0-9]+`},
		// Discord webhooks.
		{SecretDiscordHook, `https://discord(app)?\.com/api/webhooks/[0-9]+/[A-Za-z0-9_-]+`},
		// Generic credentials (key=value patterns).
		{SecretCredential, `(?i)(password|secret|auth_token|api_secret|private_key)\s*[=:]\s*["']?[^\s"']{8,}`},
		// Generic API keys.
		{SecretGenericAPI, `(?i)(api[_-]?key|apikey)\s*[=:]\s*["']?[A-Za-z0-9_-]{20,}`},
	}

	out := make([]secretRule, 0, len(rules))
	for _, r := range rules {
		re, err := regexp.Compile(r.p)
		if err != nil {
			continue
		}
		out = append(out, secretRule{Type: r.t, Pattern: re})
	}
	return out
}()

// highEntropyRe matches potential tokens: 32+ alphanumeric/special chars.
var highEntropyRe = regexp.MustCompile(`[A-Za-z0-9/+=_-]{32,}`)

// ScanMessages scans all messages and returns combined results.
func (s *Scanner) ScanMessages(msgs []ChatMessage) *ProtectResult {
	var result ProtectResult
	for _, m := range msgs {
		r := s.ScanContent(m.Content)
		result.Secrets = append(result.Secrets, r.Secrets...)
	}
	result.HasSecrets = len(result.Secrets) > 0
	return &result
}

// ScanContent scans a single content string.
func (s *Scanner) ScanContent(content string) *ProtectResult {
	var secrets []DetectedSecret

	for _, rule := range s.rules {
		matches := rule.Pattern.FindAllStringIndex(content, -1)
		for _, match := range matches {
			value := content[match[0]:match[1]]
			line := countLines(content, match[0])
			secrets = append(secrets, s.newDetectedSecret(rule.Type, value, line))
		}
	}

	// High entropy detection.
	for _, match := range highEntropyRe.FindAllStringIndex(content, -1) {
		candidate := content[match[0]:match[1]]
		if isHighEntropySecretCandidate(candidate) {
			// Skip if already caught by a specific rule.
			alreadyCaught := false
			for _, s := range secrets {
				if strings.Contains(candidate, strings.TrimSuffix(s.Value, "...")) {
					alreadyCaught = true
					break
				}
			}
			if !alreadyCaught {
				secrets = append(secrets, s.newDetectedSecret(SecretHighEntropy, candidate, countLines(content, match[0])))
			}
		}
	}

	return &ProtectResult{
		Secrets:    secrets,
		HasSecrets: len(secrets) > 0,
	}
}

func (s *Scanner) newDetectedSecret(kind SecretType, value string, line int) DetectedSecret {
	secret := DetectedSecret{
		Type:  kind,
		Value: truncateSecret(value),
		Line:  line,
	}
	if s.captureFullValues {
		secret.FullValue = value
	}
	return secret
}

// RedactMessages returns a copy of messages with secrets replaced by placeholders.
func (s *Scanner) RedactMessages(msgs []ChatMessage) ([]ChatMessage, *ProtectResult) {
	var allSecrets []DetectedSecret
	out := make([]ChatMessage, len(msgs))

	for i, m := range msgs {
		result := s.ScanContent(m.Content)
		allSecrets = append(allSecrets, result.Secrets...)

		content := m.Content
		if result.HasSecrets {
			content = s.redactContent(content)
		}
		out[i] = ChatMessage{Role: m.Role, Content: content, Source: m.Source}
	}

	return out, &ProtectResult{
		Secrets:    allSecrets,
		HasSecrets: len(allSecrets) > 0,
	}
}

func (s *Scanner) redactContent(content string) string {
	counters := make(map[SecretType]int)

	// Apply specific rules first.
	for _, rule := range s.rules {
		content = rule.Pattern.ReplaceAllStringFunc(content, func(match string) string {
			counters[rule.Type]++
			typeName := strings.ToUpper(string(rule.Type))
			return "<" + typeName + "_" + itoa(counters[rule.Type]) + ">"
		})
	}

	// High entropy.
	content = highEntropyRe.ReplaceAllStringFunc(content, func(match string) string {
		if isHighEntropySecretCandidate(match) {
			counters[SecretHighEntropy]++
			return "<HIGH_ENTROPY_" + itoa(counters[SecretHighEntropy]) + ">"
		}
		return match
	})

	return content
}

func truncateSecret(s string) string {
	if len(s) <= 8 {
		return s[:len(s)/2] + "..."
	}
	return s[:8] + "..."
}

func cloneDetectedSecrets(secrets []DetectedSecret) []DetectedSecret {
	if len(secrets) == 0 {
		return nil
	}
	out := make([]DetectedSecret, len(secrets))
	copy(out, secrets)
	return out
}

func countLines(content string, offset int) int {
	line := 1
	for i := 0; i < offset && i < len(content); i++ {
		if content[i] == '\n' {
			line++
		}
	}
	return line
}

func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	freq := make(map[byte]int)
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	n := float64(len(s))
	entropy := 0.0
	for _, count := range freq {
		p := float64(count) / n
		if p > 0 {
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}

func hasMixedCharClasses(s string) bool {
	var hasUpper, hasLower, hasDigit bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			hasUpper = true
		} else if c >= 'a' && c <= 'z' {
			hasLower = true
		} else if c >= '0' && c <= '9' {
			hasDigit = true
		}
	}
	count := 0
	if hasUpper {
		count++
	}
	if hasLower {
		count++
	}
	if hasDigit {
		count++
	}
	return count >= 2
}

func isHighEntropySecretCandidate(s string) bool {
	if shannonEntropy(s) <= 4.5 || !hasMixedCharClasses(s) {
		return false
	}
	if looksLikePathLikeIdentifier(s) {
		return false
	}
	return true
}

func looksLikePathLikeIdentifier(s string) bool {
	if strings.Count(s, "/") < 2 {
		return false
	}

	segments := 0
	humanish := 0
	for _, segment := range strings.Split(s, "/") {
		if segment == "" {
			continue
		}
		segments++
		if isHumanishPathSegment(segment) {
			humanish++
		}
	}

	return segments >= 3 && humanish >= 2
}

func isHumanishPathSegment(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	if shannonEntropy(s) > 4.0 {
		return false
	}

	hasLetter := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			hasLetter = true
		case c >= 'a' && c <= 'z':
			hasLetter = true
		case c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.':
		default:
			return false
		}
	}

	return hasLetter
}

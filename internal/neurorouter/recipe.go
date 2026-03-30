package neurorouter

// RotationRecipe describes how to rotate a specific credential type.
type RotationRecipe struct {
	Type           SecretType `json:"type"`            // matches SecretType from protect.go
	Name           string     `json:"name"`            // human-readable name
	Severity       string     `json:"severity"`        // critical, high, medium, low
	RotateSteps    []string   `json:"rotate_steps"`    // ordered CLI commands to rotate
	DocsURL        string     `json:"docs_url"`        // documentation link
	PreventionHook string     `json:"prevention_hook"` // how to prevent this in future
}

// RecipeCatalog holds all rotation recipes, indexed by SecretType.
type RecipeCatalog struct {
	recipes map[SecretType]RotationRecipe
}

// NewRecipeCatalog creates a catalog with all built-in recipes.
func NewRecipeCatalog() *RecipeCatalog {
	c := &RecipeCatalog{recipes: make(map[SecretType]RotationRecipe)}
	for _, r := range builtinRecipes {
		c.recipes[r.Type] = r
	}
	return c
}

// Lookup returns the rotation recipe for a secret type, if one exists.
func (c *RecipeCatalog) Lookup(t SecretType) (RotationRecipe, bool) {
	r, ok := c.recipes[t]
	return r, ok
}

// Add adds or overrides a recipe (for user-defined custom recipes).
func (c *RecipeCatalog) Add(r RotationRecipe) {
	c.recipes[r.Type] = r
}

// All returns all recipes in the catalog.
func (c *RecipeCatalog) All() []RotationRecipe {
	out := make([]RotationRecipe, 0, len(c.recipes))
	for _, r := range c.recipes {
		out = append(out, r)
	}
	return out
}

var builtinRecipes = []RotationRecipe{
	{
		Type:     SecretAWSKey,
		Name:     "AWS Access Key",
		Severity: "critical",
		RotateSteps: []string{
			"aws iam create-access-key --user-name <USER>",
			"# Update the new key in your application/env",
			"aws iam delete-access-key --user-name <USER> --access-key-id <OLD_KEY_ID>",
			"aws cloudtrail lookup-events --lookup-attributes AttributeKey=AccessKeyId,AttributeValue=<OLD_KEY_ID> --max-results 10",
		},
		DocsURL:        "https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_access-keys.html#rotating_access_keys_console",
		PreventionHook: "use IAM roles or AWS SSO instead of long-lived access keys",
	},
	{
		Type:     SecretGitHubToken,
		Name:     "GitHub Personal Access Token",
		Severity: "critical",
		RotateSteps: []string{
			"# Go to https://github.com/settings/tokens",
			"# Revoke the compromised token",
			"# Create a new token with minimum required scopes",
			"gh auth refresh",
			"# Review https://github.com/settings/security for unauthorized access",
		},
		DocsURL:        "https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens",
		PreventionHook: "use gh auth token or GITHUB_TOKEN from CI, never hardcode PATs",
	},
	{
		Type:     SecretStripeKey,
		Name:     "Stripe API Key",
		Severity: "critical",
		RotateSteps: []string{
			"# Go to https://dashboard.stripe.com/apikeys",
			"# Roll the secret key (creates new key, old key expires)",
			"# Update the new key in your application",
			"# Review recent charges: https://dashboard.stripe.com/payments",
		},
		DocsURL:        "https://docs.stripe.com/keys#rolling-keys",
		PreventionHook: "use restricted keys with minimum permissions, never use sk_live_ in code",
	},
	{
		Type:     SecretDBConn,
		Name:     "Database Connection String",
		Severity: "critical",
		RotateSteps: []string{
			"# Change the database password immediately",
			"# PostgreSQL: ALTER USER <user> PASSWORD '<new_password>';",
			"# MySQL: ALTER USER '<user>'@'%' IDENTIFIED BY '<new_password>';",
			"# Update connection string in secret manager (not env/code)",
			"# Review database audit logs for unauthorized queries",
			"# Check for new users or privilege escalations",
		},
		DocsURL:        "https://www.postgresql.org/docs/current/sql-alterrole.html",
		PreventionHook: "store DSN in secret manager (Vault, AWS SSM, 1Password), reference by name ($DB_DSN)",
	},
	{
		Type:     SecretSSHKey,
		Name:     "SSH Private Key",
		Severity: "critical",
		RotateSteps: []string{
			"ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519_new -C 'rotated-$(date +%Y%m%d)'",
			"# Add public key to authorized_keys on all target hosts",
			"# Remove the old public key from authorized_keys",
			"# Delete the compromised private key",
			"# Update SSH agent: ssh-add ~/.ssh/id_ed25519_new",
		},
		DocsURL:        "https://docs.github.com/en/authentication/connecting-to-github-with-ssh/generating-a-new-ssh-key-and-adding-it-to-the-ssh-agent",
		PreventionHook: "use ssh-agent, never embed keys in code or paste them into prompts",
	},
	{
		Type:     SecretJWT,
		Name:     "JWT Token / Signing Secret",
		Severity: "critical",
		RotateSteps: []string{
			"# Rotate the JWT signing secret in your auth service",
			"# Invalidate all existing tokens (force re-authentication)",
			"# If a bearer token was leaked: revoke it in your token store",
			"# Review auth logs for token misuse",
		},
		DocsURL:        "https://auth0.com/docs/secure/tokens/json-web-tokens/rotate-signing-keys",
		PreventionHook: "never log or expose JWT tokens, use short expiry (15min) with refresh tokens",
	},
	{
		Type:     SecretOpenAIKey,
		Name:     "OpenAI API Key",
		Severity: "critical",
		RotateSteps: []string{
			"# Go to https://platform.openai.com/api-keys",
			"# Delete the compromised key",
			"# Create a new key with the same permissions",
			"# Update OPENAI_API_KEY in your environment",
			"# Review usage: https://platform.openai.com/usage",
		},
		DocsURL:        "https://platform.openai.com/docs/api-reference/authentication",
		PreventionHook: "use environment variables, never hardcode API keys",
	},
	{
		Type:     SecretAnthropicKey,
		Name:     "Anthropic API Key",
		Severity: "critical",
		RotateSteps: []string{
			"# Go to https://console.anthropic.com/settings/keys",
			"# Delete the compromised key",
			"# Create a new key",
			"# Update ANTHROPIC_API_KEY in your environment",
			"# Review usage in the console",
		},
		DocsURL:        "https://docs.anthropic.com/en/docs/initial-setup#set-your-api-key",
		PreventionHook: "use environment variables, never hardcode API keys",
	},
	{
		Type:     SecretSlackWebhook,
		Name:     "Slack Incoming Webhook",
		Severity: "high",
		RotateSteps: []string{
			"# Go to https://api.slack.com/apps → your app → Incoming Webhooks",
			"# Delete the compromised webhook URL",
			"# Create a new webhook for the same channel",
			"# Update the webhook URL in your application",
		},
		DocsURL:        "https://api.slack.com/messaging/webhooks",
		PreventionHook: "store webhook URLs in secret manager, not in code or config files",
	},
	{
		Type:     SecretCredential,
		Name:     "Generic Credential (password/secret/auth_token)",
		Severity: "critical",
		RotateSteps: []string{
			"# Identify the service this credential belongs to",
			"# Change the password/secret in that service",
			"# Update all systems that use this credential",
			"# Review access logs for unauthorized use",
			"# Check if the credential was committed to git: git log -p -S '<partial_value>'",
		},
		DocsURL:        "",
		PreventionHook: "use keyword format (password=, secret=) so detection tools catch it; store in secret manager",
	},
	{
		Type:     SecretGenericAPI,
		Name:     "Generic API Key",
		Severity: "high",
		RotateSteps: []string{
			"# Identify the service this API key belongs to",
			"# Revoke the key in that service's dashboard",
			"# Generate a new key with minimum required permissions",
			"# Update the key in your environment/secret manager",
		},
		DocsURL:        "",
		PreventionHook: "use environment variables (env:VAR_NAME), never inline API keys",
	},
	{
		Type:     SecretGroqKey,
		Name:     "Groq API Key",
		Severity: "critical",
		RotateSteps: []string{
			"# Go to https://console.groq.com/keys",
			"# Delete the compromised key",
			"# Create a new key",
			"# Update GROQ_API_KEY in your environment",
		},
		DocsURL:        "https://console.groq.com/docs/api-keys",
		PreventionHook: "use environment variables, never hardcode API keys",
	},
	{
		Type:     SecretDiscordHook,
		Name:     "Discord Webhook",
		Severity: "high",
		RotateSteps: []string{
			"# Go to Discord Server Settings → Integrations → Webhooks",
			"# Delete the compromised webhook",
			"# Create a new webhook for the same channel",
			"# Update the webhook URL in your application",
		},
		DocsURL:        "https://discord.com/developers/docs/resources/webhook",
		PreventionHook: "store webhook URLs in secret manager, not in code",
	},
	{
		Type:     SecretNPMToken,
		Name:     "npm Access Token",
		Severity: "critical",
		RotateSteps: []string{
			"npm token revoke <token>",
			"npm token create",
			"# Update .npmrc or CI secrets with the new token",
			"# Review published packages for unauthorized versions",
		},
		DocsURL:        "https://docs.npmjs.com/creating-and-viewing-access-tokens",
		PreventionHook: "use npm automation tokens with publish-only scope, never use full-access tokens",
	},
	{
		Type:     SecretGitLabToken,
		Name:     "GitLab Personal Access Token",
		Severity: "critical",
		RotateSteps: []string{
			"# Go to https://gitlab.com/-/user_settings/personal_access_tokens",
			"# Revoke the compromised token",
			"# Create a new token with minimum required scopes",
			"# Update CI/CD variables or local config",
		},
		DocsURL:        "https://docs.gitlab.com/ee/user/profile/personal_access_tokens.html",
		PreventionHook: "use project/group access tokens for CI, not personal tokens",
	},
}

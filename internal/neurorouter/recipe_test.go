package neurorouter

import "testing"

func TestRecipeCatalog_BuiltinCount(t *testing.T) {
	c := NewRecipeCatalog()
	all := c.All()
	if len(all) < 10 {
		t.Errorf("expected at least 10 builtin recipes, got %d", len(all))
	}
}

func TestRecipeCatalog_Lookup(t *testing.T) {
	c := NewRecipeCatalog()

	tests := []SecretType{
		SecretAWSKey, SecretGitHubToken, SecretStripeKey, SecretDBConn,
		SecretSSHKey, SecretJWT, SecretOpenAIKey, SecretAnthropicKey,
		SecretSlackWebhook, SecretCredential, SecretGenericAPI,
	}

	for _, typ := range tests {
		r, ok := c.Lookup(typ)
		if !ok {
			t.Errorf("missing recipe for %s", typ)
			continue
		}
		if r.Name == "" {
			t.Errorf("recipe for %s has empty Name", typ)
		}
		if r.Severity == "" {
			t.Errorf("recipe for %s has empty Severity", typ)
		}
		if len(r.RotateSteps) == 0 {
			t.Errorf("recipe for %s has no RotateSteps", typ)
		}
		if r.PreventionHook == "" {
			t.Errorf("recipe for %s has empty PreventionHook", typ)
		}
	}
}

func TestRecipeCatalog_LookupMissing(t *testing.T) {
	c := NewRecipeCatalog()
	_, ok := c.Lookup(SecretHighEntropy)
	if ok {
		t.Error("expected no recipe for high_entropy (no specific rotation)")
	}
}

func TestRecipeCatalog_CustomRecipe(t *testing.T) {
	c := NewRecipeCatalog()

	custom := RotationRecipe{
		Type:           SecretHighEntropy,
		Name:           "Custom High Entropy Secret",
		Severity:       "medium",
		RotateSteps:    []string{"identify the service", "rotate the credential"},
		PreventionHook: "use structured secret formats with known prefixes",
	}
	c.Add(custom)

	r, ok := c.Lookup(SecretHighEntropy)
	if !ok {
		t.Fatal("expected custom recipe to be found")
	}
	if r.Name != "Custom High Entropy Secret" {
		t.Errorf("expected custom name, got %s", r.Name)
	}
}

func TestRecipeCatalog_OverrideBuiltin(t *testing.T) {
	c := NewRecipeCatalog()

	override := RotationRecipe{
		Type:           SecretAWSKey,
		Name:           "Custom AWS Rotation",
		Severity:       "high",
		RotateSteps:    []string{"custom step 1"},
		PreventionHook: "custom prevention",
	}
	c.Add(override)

	r, _ := c.Lookup(SecretAWSKey)
	if r.Name != "Custom AWS Rotation" {
		t.Error("override did not take effect")
	}
}

func TestRecipeCatalog_AllFieldsPopulated(t *testing.T) {
	c := NewRecipeCatalog()
	for _, r := range c.All() {
		if r.Type == "" {
			t.Error("recipe has empty Type")
		}
		if r.Name == "" {
			t.Errorf("recipe %s has empty Name", r.Type)
		}
		if r.Severity == "" {
			t.Errorf("recipe %s has empty Severity", r.Type)
		}
		if len(r.RotateSteps) == 0 {
			t.Errorf("recipe %s has no RotateSteps", r.Type)
		}
		if r.PreventionHook == "" {
			t.Errorf("recipe %s has empty PreventionHook", r.Type)
		}
	}
}

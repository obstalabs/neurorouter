package neurorouter

import "testing"

func TestHygiene_VagueDirective_CleanUp(t *testing.T) {
	hd := NewHygieneDetector()
	msgs := []ChatMessage{{Role: "user", Content: "clean up the code"}}
	check := hd.Check(msgs)
	if check == nil {
		t.Fatal("expected vague_directive check")
	}
	if check.Issue != "vague_directive" {
		t.Errorf("expected vague_directive, got %s", check.Issue)
	}
}

func TestHygiene_VagueDirective_FixEverything(t *testing.T) {
	hd := NewHygieneDetector()
	msgs := []ChatMessage{{Role: "user", Content: "fix everything"}}
	check := hd.Check(msgs)
	if check == nil {
		t.Fatal("expected vague_directive check")
	}
}

func TestHygiene_VagueDirective_MakeItBetter(t *testing.T) {
	hd := NewHygieneDetector()
	msgs := []ChatMessage{{Role: "user", Content: "make it better"}}
	check := hd.Check(msgs)
	if check == nil {
		t.Fatal("expected vague_directive check")
	}
}

func TestHygiene_SharpPromptPasses(t *testing.T) {
	hd := NewHygieneDetector()
	sharp := []string{
		"in proxy.go line 42, change the timeout from 10s to 30s",
		"add a --verbose flag to the proxy command that enables debug logging",
		"rename FilterStaleReads to FilterDuplicateReads in filter.go",
	}
	for _, prompt := range sharp {
		hd.Reset()
		msgs := []ChatMessage{{Role: "user", Content: prompt}}
		check := hd.Check(msgs)
		if check != nil {
			t.Errorf("sharp prompt flagged: %q → %s", prompt, check.Issue)
		}
	}
}

func TestHygiene_ParasiteWords(t *testing.T) {
	hd := NewHygieneDetector()
	msgs := []ChatMessage{{Role: "user", Content: "I think maybe we should just sort of basically clean up the stuff in the handler"}}
	check := hd.Check(msgs)
	if check == nil {
		t.Fatal("expected parasite_words check")
	}
	if check.Issue != "parasite_words" {
		t.Errorf("expected parasite_words, got %s", check.Issue)
	}
}

func TestHygiene_FewParasiteWordsPass(t *testing.T) {
	hd := NewHygieneDetector()
	// Only 1 parasite word — below threshold.
	msgs := []ChatMessage{{Role: "user", Content: "just add error handling to the handler function please"}}
	check := hd.Check(msgs)
	if check != nil && check.Issue == "parasite_words" {
		t.Error("1 parasite word should not trigger")
	}
}

func TestHygiene_MissingContext(t *testing.T) {
	hd := NewHygieneDetector()
	msgs := []ChatMessage{{Role: "user", Content: "add logging"}}
	check := hd.Check(msgs)
	if check == nil {
		t.Fatal("expected missing_context check")
	}
	if check.Issue != "missing_context" {
		t.Errorf("expected missing_context, got %s", check.Issue)
	}
}

func TestHygiene_ThrottlesAfterFirstFire(t *testing.T) {
	hd := NewHygieneDetector()

	// First vague prompt fires.
	msgs1 := []ChatMessage{{Role: "user", Content: "clean up the code"}}
	check1 := hd.Check(msgs1)
	if check1 == nil {
		t.Fatal("expected first check to fire")
	}

	// Second vague prompt does NOT fire (throttled).
	msgs2 := []ChatMessage{{Role: "user", Content: "fix everything"}}
	check2 := hd.Check(msgs2)
	if check2 != nil {
		t.Error("should throttle after first fire (max 1 per session)")
	}
}

func TestHygiene_Reset(t *testing.T) {
	hd := NewHygieneDetector()

	msgs := []ChatMessage{{Role: "user", Content: "clean up the code"}}
	hd.Check(msgs) // fires

	hd.Reset()

	check := hd.Check(msgs) // should fire again after reset
	if check == nil {
		t.Error("should fire again after reset")
	}
}

func TestHygiene_IgnoresToolResults(t *testing.T) {
	hd := NewHygieneDetector()
	msgs := []ChatMessage{
		{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"t1","content":"error: stuff broke"}]`},
	}
	check := hd.Check(msgs)
	if check != nil {
		t.Error("should ignore tool_result messages")
	}
}

func TestHygiene_UsesLastUserMessage(t *testing.T) {
	hd := NewHygieneDetector()
	msgs := []ChatMessage{
		{Role: "user", Content: "clean up the code"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "in proxy.go, rename handleResponses to HandleResponses"},
	}
	check := hd.Check(msgs)
	// Last user message is sharp — should not fire.
	if check != nil {
		t.Error("should use last user message, which is sharp")
	}
}

func TestFormatHygieneCheck(t *testing.T) {
	check := &HygieneCheck{
		Issue:      "vague_directive",
		Original:   "clean up",
		Suggestion: "specify which file",
	}
	out := FormatHygieneCheck(check)
	if out == "" {
		t.Error("expected non-empty format")
	}
	if !containsStr(out, "clean up") {
		t.Error("expected original in output")
	}
}

func TestFormatHygieneCheck_Nil(t *testing.T) {
	if FormatHygieneCheck(nil) != "" {
		t.Error("nil check should return empty string")
	}
}

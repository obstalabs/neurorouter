package neurorouter

import "testing"

func TestSummarizeSavings(t *testing.T) {
	summary := SummarizeSavings(10000, 5000, 6.0)

	if summary.BytesSaved != 5000 {
		t.Fatalf("bytes saved: got %d, want 5000", summary.BytesSaved)
	}
	if summary.TokensSaved != 1250 {
		t.Fatalf("tokens saved: got %d, want 1250", summary.TokensSaved)
	}
	if summary.MoneySavedUSD != 0.0075 {
		t.Fatalf("money saved: got %.4f, want 0.0075", summary.MoneySavedUSD)
	}
	if summary.SavedPercent != 50 {
		t.Fatalf("saved percent: got %d, want 50", summary.SavedPercent)
	}
}

func TestBuildSavingsFingerprint(t *testing.T) {
	fingerprint := BuildSavingsFingerprint(72628, 64030, []string{"oversized_blocks", "oversized_blocks"})

	if fingerprint.Key != "oversized_blocks/8598B" {
		t.Fatalf("key: got %q, want oversized_blocks/8598B", fingerprint.Key)
	}
	if fingerprint.Label != "fixed-delta oversized_blocks/8598B" {
		t.Fatalf("label: got %q", fingerprint.Label)
	}
}

func TestBuildSavingsFingerprint_IgnoresNonSavings(t *testing.T) {
	if fingerprint := BuildSavingsFingerprint(100, 100, []string{"oversized_blocks"}); fingerprint.Key != "" {
		t.Fatalf("expected empty fingerprint, got %+v", fingerprint)
	}
	if fingerprint := BuildSavingsFingerprint(100, 50, nil); fingerprint.Key != "" {
		t.Fatalf("expected empty fingerprint without filters, got %+v", fingerprint)
	}
}

func TestFormatRecurringSavingsFingerprint(t *testing.T) {
	fingerprint := SavingsFingerprint{Key: "oversized_blocks/8598B", Label: "fixed-delta oversized_blocks/8598B"}

	if got := FormatRecurringSavingsFingerprint(fingerprint, 1); got != "" {
		t.Fatalf("count 1: got %q, want empty", got)
	}
	if got := FormatRecurringSavingsFingerprint(fingerprint, 3); got != "fixed-delta oversized_blocks/8598B x3" {
		t.Fatalf("count 3: got %q", got)
	}
}

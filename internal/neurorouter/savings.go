package neurorouter

import (
	"fmt"
	"sort"
	"strings"
)

const (
	DefaultInputPricePerMillionUSD = 3.0
	defaultBytesPerToken           = 4
)

type SavingsSummary struct {
	BytesBefore   int
	BytesAfter    int
	BytesSaved    int
	TokensSaved   int
	MoneySavedUSD float64
	SavedPercent  int
}

type SavingsFingerprint struct {
	Key   string
	Label string
}

func NormalizeInputPricePerMillionUSD(price float64) float64 {
	if price <= 0 {
		return DefaultInputPricePerMillionUSD
	}
	return price
}

func TokensFromBytes(bytes int64) int {
	if bytes <= 0 {
		return 0
	}
	return int(bytes / defaultBytesPerToken)
}

func MoneySavedUSD(tokens int, inputPricePerMillionUSD float64) float64 {
	if tokens <= 0 {
		return 0
	}
	price := NormalizeInputPricePerMillionUSD(inputPricePerMillionUSD)
	return float64(tokens) * price / 1_000_000
}

func SummarizeSavings(before, after int, inputPricePerMillionUSD float64) SavingsSummary {
	summary := SavingsSummary{
		BytesBefore: before,
		BytesAfter:  after,
		BytesSaved:  before - after,
	}
	if summary.BytesSaved <= 0 {
		return summary
	}
	summary.TokensSaved = TokensFromBytes(int64(summary.BytesSaved))
	summary.MoneySavedUSD = MoneySavedUSD(summary.TokensSaved, inputPricePerMillionUSD)
	if before > 0 {
		summary.SavedPercent = summary.BytesSaved * 100 / before
	}
	return summary
}

func BuildSavingsFingerprint(before, after int, filters []string) SavingsFingerprint {
	summary := SummarizeSavings(before, after, DefaultInputPricePerMillionUSD)
	if summary.BytesSaved <= 0 {
		return SavingsFingerprint{}
	}

	filterLabel := normalizedSavingsFingerprintFilters(filters)
	if filterLabel == "" {
		return SavingsFingerprint{}
	}

	key := fmt.Sprintf("%s/%dB", filterLabel, summary.BytesSaved)
	return SavingsFingerprint{
		Key:   key,
		Label: "fixed-delta " + key,
	}
}

func FormatRecurringSavingsFingerprint(fingerprint SavingsFingerprint, count int) string {
	if fingerprint.Key == "" || count < 2 {
		return ""
	}
	return fmt.Sprintf("%s x%d", fingerprint.Label, count)
}

func normalizedSavingsFingerprintFilters(filters []string) string {
	if len(filters) == 0 {
		return ""
	}

	seen := make(map[string]bool, len(filters))
	normalized := make([]string, 0, len(filters))
	for _, filter := range filters {
		filter = strings.TrimSpace(filter)
		if filter == "" || seen[filter] {
			continue
		}
		seen[filter] = true
		normalized = append(normalized, filter)
	}
	if len(normalized) == 0 {
		return ""
	}

	sort.Strings(normalized)
	return strings.Join(normalized, "+")
}

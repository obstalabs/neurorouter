package main

import (
	"fmt"

	"github.com/obstalabs/neurorouter/internal/neurorouter"
	"github.com/spf13/cobra"
)

func resolveInputPricePerMillionUSD(cmd *cobra.Command, cfg *neurorouter.Config) float64 {
	price := neurorouter.DefaultInputPricePerMillionUSD
	if cfg != nil {
		price = cfg.InputPricePerMillionUSD
	}
	if cmd.Flags().Changed("input-price-per-million-usd") {
		price = flagFloat(cmd, "input-price-per-million-usd")
	}
	return neurorouter.NormalizeInputPricePerMillionUSD(price)
}

func formatKB(bytes int) string {
	if bytes < 0 {
		bytes = 0
	}
	return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
}

func formatRequestContextLabel(before, after int) string {
	if before > 0 && before != after {
		return fmt.Sprintf("context=%s (shaped from %s)", formatKB(after), formatKB(before))
	}
	return fmt.Sprintf("context=%s", formatKB(after))
}

func formatRequestSummary(before, after int, inputPricePerMillionUSD float64) string {
	summary := neurorouter.SummarizeSavings(before, after, inputPricePerMillionUSD)
	delta := formatRequestDelta(before, after)
	if summary.BytesSaved <= 0 {
		return delta
	}
	return fmt.Sprintf("%s; %d tokens; $%.4f avoided", delta, summary.TokensSaved, summary.MoneySavedUSD)
}

func trackRecurringFingerprint(counts map[string]int, before, after int, filters []string) string {
	fingerprint := neurorouter.BuildSavingsFingerprint(before, after, filters)
	if fingerprint.Key == "" {
		return ""
	}
	counts[fingerprint.Key]++
	return neurorouter.FormatRecurringSavingsFingerprint(fingerprint, counts[fingerprint.Key])
}

func topRecurringFingerprint(entries []neurorouter.AuditEntry) string {
	bestCount := 0
	bestLabel := ""
	counts := make(map[string]int)
	labels := make(map[string]string)

	for _, entry := range entries {
		fingerprint := neurorouter.BuildSavingsFingerprint(entry.BytesBefore, entry.BytesAfter, entry.FiltersRun)
		if fingerprint.Key == "" {
			continue
		}
		counts[fingerprint.Key]++
		labels[fingerprint.Key] = fingerprint.Label
		if counts[fingerprint.Key] > bestCount {
			bestCount = counts[fingerprint.Key]
			bestLabel = neurorouter.FormatRecurringSavingsFingerprint(fingerprint, counts[fingerprint.Key])
			continue
		}
		if counts[fingerprint.Key] == bestCount && bestLabel == "" {
			bestLabel = neurorouter.FormatRecurringSavingsFingerprint(fingerprint, counts[fingerprint.Key])
		}
	}

	if bestCount < 2 {
		return ""
	}
	if bestLabel != "" {
		return bestLabel
	}
	for key, count := range counts {
		if count == bestCount {
			return fmt.Sprintf("%s x%d", labels[key], count)
		}
	}
	return ""
}

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/obstalabs/neurorouter/internal/neurorouter"
	"github.com/spf13/cobra"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show session statistics from the running proxy",
	RunE:  runStats,
}

func init() {
	statsCmd.Flags().String("addr", "localhost:4000", "proxy address to query")
	statsCmd.Flags().Bool("json", false, "output as JSON")
	statsCmd.Flags().String("session", "", "session identifier to inspect")
	statsCmd.Flags().Float64("input-price-per-million-usd", neurorouter.DefaultInputPricePerMillionUSD, "estimated input token price used for savings telemetry")
}

func runStats(cmd *cobra.Command, _ []string) error {
	addr, _ := cmd.Flags().GetString("addr")
	jsonOut, _ := cmd.Flags().GetBool("json")
	session, _ := cmd.Flags().GetString("session")
	out := cmd.OutOrStdout()
	priceFlagChanged := cmd.Flags().Changed("input-price-per-million-usd")
	cfg, err := neurorouter.LoadConfig("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	inputPricePerMillionUSD := resolveInputPricePerMillionUSD(cmd, cfg)

	// Fetch suggestions.
	sugResp, err := http.Get(managementURL(addr, "/v1/suggestions", session))
	if err != nil {
		return fmt.Errorf("connect to proxy at %s: %w", addr, err)
	}
	defer func() { _ = sugResp.Body.Close() }()
	if sugResp.StatusCode != http.StatusOK {
		return fmt.Errorf("suggestions endpoint unavailable at %s (status %d): use the default loopback bind or start the proxy with --public --expose-management", addr, sugResp.StatusCode)
	}
	sugBody, _ := io.ReadAll(sugResp.Body)

	// Fetch audit.
	auditResp, err := http.Get(managementURL(addr, "/v1/audit", session))
	if err != nil {
		return fmt.Errorf("fetch audit: %w", err)
	}
	defer func() { _ = auditResp.Body.Close() }()
	if auditResp.StatusCode != http.StatusOK {
		return fmt.Errorf("audit endpoint unavailable at %s (status %d): use the default loopback bind or start the proxy with --public --expose-management", addr, auditResp.StatusCode)
	}
	auditBody, _ := io.ReadAll(auditResp.Body)

	if jsonOut {
		_, err := fmt.Fprintf(out, "{\"suggestions\":%s,\"audit\":%s}\n", sugBody, auditBody)
		return err
	}

	// Parse and display human-readable.
	var sugData struct {
		Suggestions []struct {
			Type     string `json:"type"`
			Severity string `json:"severity"`
			Metric   string `json:"metric"`
			Action   string `json:"action"`
		} `json:"suggestions"`
	}
	_ = json.Unmarshal(sugBody, &sugData)

	var auditData struct {
		Count                   int                      `json:"count"`
		Entries                 []neurorouter.AuditEntry `json:"entries"`
		InputPricePerMillionUSD float64                  `json:"input_price_per_million_usd"`
	}
	_ = json.Unmarshal(auditBody, &auditData)
	if !priceFlagChanged && auditData.InputPricePerMillionUSD > 0 {
		inputPricePerMillionUSD = neurorouter.NormalizeInputPricePerMillionUSD(auditData.InputPricePerMillionUSD)
	}

	// Aggregate from audit entries.
	totalReqs := auditData.Count
	var totalBefore, totalAfter, totalSecrets int
	filterHits := make(map[string]int)
	for _, e := range auditData.Entries {
		totalBefore += e.BytesBefore
		totalAfter += e.BytesAfter
		totalSecrets += e.SecretsFound
		for _, f := range e.FiltersRun {
			filterHits[f]++
		}
	}

	if _, err := fmt.Fprintf(out, "Session stats (%d requests)\n", totalReqs); err != nil {
		return err
	}
	if totalReqs == 0 {
		_, err := fmt.Fprintln(out, "  No requests yet. Send traffic through the proxy to see stats.")
		return err
	}

	summary := neurorouter.SummarizeSavings(totalBefore, totalAfter, inputPricePerMillionUSD)
	if _, err := fmt.Fprintf(out, "  Bytes: %dKB → %dKB (%d%% saved, ~$%.4f saved)\n",
		totalBefore/1024, totalAfter/1024, summary.SavedPercent, summary.MoneySavedUSD); err != nil {
		return err
	}

	// Top filter.
	topFilter := ""
	topHits := 0
	for f, c := range filterHits {
		if c > topHits {
			topFilter = f
			topHits = c
		}
	}
	if topFilter != "" {
		if _, err := fmt.Fprintf(out, "  Top filter: %s (%d activations)\n", topFilter, topHits); err != nil {
			return err
		}
	}
	if recurring := topRecurringFingerprint(auditData.Entries); recurring != "" {
		if _, err := fmt.Fprintf(out, "  Recurring: %s\n", recurring); err != nil {
			return err
		}
	}

	if totalSecrets > 0 {
		if _, err := fmt.Fprintf(out, "  Secrets caught: %d\n", totalSecrets); err != nil {
			return err
		}
	}

	if len(sugData.Suggestions) > 0 {
		if _, err := fmt.Fprintf(out, "  Suggestions: %d\n\n", len(sugData.Suggestions)); err != nil {
			return err
		}
		for _, s := range sugData.Suggestions {
			if _, err := fmt.Fprintf(out, "  [%s] %s: %s\n", s.Severity, s.Type, s.Metric); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "         → %s\n", s.Action); err != nil {
				return err
			}
		}
	}

	return nil
}

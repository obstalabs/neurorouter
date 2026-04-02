package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ppiankov/neurorouter/internal/neurorouter"
	"github.com/spf13/cobra"
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Show transformation audit log from the running proxy",
	RunE:  runAudit,
}

func init() {
	auditCmd.Flags().String("addr", "localhost:4000", "proxy address to query")
	auditCmd.Flags().Int("last", 10, "number of entries to show")
	auditCmd.Flags().Bool("json", false, "output as JSON")
	auditCmd.Flags().String("session", "", "session identifier to inspect")
	auditCmd.Flags().String("secret-report", secretReportOff, "secret diagnostics mode: off, redacted")
}

func runAudit(cmd *cobra.Command, _ []string) error {
	addr, _ := cmd.Flags().GetString("addr")
	last, _ := cmd.Flags().GetInt("last")
	jsonOut, _ := cmd.Flags().GetBool("json")
	session, _ := cmd.Flags().GetString("session")
	secretReportFlag, _ := cmd.Flags().GetString("secret-report")
	out := cmd.OutOrStdout()
	secretReport, err := normalizeSecretReportMode(secretReportFlag)
	if err != nil {
		return err
	}

	resp, err := http.Get(auditManagementURL(addr, session, secretReport))
	if err != nil {
		return fmt.Errorf("connect to proxy at %s: %w", addr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("audit endpoint unavailable at %s (status %d): use the default loopback bind or start the proxy with --public --expose-management", addr, resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)

	if jsonOut {
		_, err := fmt.Fprintln(out, string(body))
		return err
	}

	var data struct {
		Count   int `json:"count"`
		Entries []struct {
			Timestamp         string                       `json:"timestamp"`
			Model             string                       `json:"model"`
			BytesBefore       int                          `json:"bytes_before"`
			BytesAfter        int                          `json:"bytes_after"`
			FiltersRun        []string                     `json:"filters_run"`
			SecretsFound      int                          `json:"secrets_found"`
			SecretDiagnostics []neurorouter.DetectedSecret `json:"secret_diagnostics"`
			SecretPolicy      string                       `json:"secret_policy"`
			Blocked           bool                         `json:"blocked"`
		} `json:"entries"`
	}
	_ = json.Unmarshal(body, &data)

	if data.Count == 0 {
		_, err := fmt.Fprintln(out, "No audit entries yet.")
		return err
	}

	// Show last N entries.
	entries := data.Entries
	if len(entries) > last {
		entries = entries[len(entries)-last:]
	}

	if _, err := fmt.Fprintf(out, "Last %d transformations:\n", len(entries)); err != nil {
		return err
	}
	for _, e := range entries {
		// Extract time portion.
		ts := e.Timestamp
		if len(ts) > 19 {
			ts = ts[11:19] // HH:MM:SS
		}

		savedPct := 0
		if e.BytesBefore > 0 {
			savedPct = (e.BytesBefore - e.BytesAfter) * 100 / e.BytesBefore
		}

		filters := ""
		if len(e.FiltersRun) > 0 {
			filters = "  filters=[" + strings.Join(e.FiltersRun, ",") + "]"
		}

		secrets := ""
		if e.SecretsFound > 0 {
			secrets = fmt.Sprintf("  secrets=%d (%s)", e.SecretsFound, e.SecretPolicy)
		}

		blocked := ""
		if e.Blocked {
			blocked = "  BLOCKED"
		}

		if _, err := fmt.Fprintf(out, "  %s  %s  %.1fKB → %.1fKB  [%+d%%]%s%s%s\n",
			ts, e.Model,
			float64(e.BytesBefore)/1024, float64(e.BytesAfter)/1024,
			-savedPct, filters, secrets, blocked); err != nil {
			return err
		}
		if secretReport == secretReportRedacted && len(e.SecretDiagnostics) > 0 {
			printSecretDiagnostics(out, e.SecretDiagnostics)
		}
	}

	return nil
}

func auditManagementURL(addr, session, secretReport string) string {
	base := managementURL(addr, "/v1/audit", session)
	if secretReport != secretReportRedacted {
		return base
	}
	if strings.Contains(base, "?") {
		return base + "&secret_report=" + secretReport
	}
	return base + "?secret_report=" + secretReport
}

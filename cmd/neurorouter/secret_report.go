package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/ppiankov/neurorouter/internal/neurorouter"
)

const (
	secretReportOff      = "off"
	secretReportRedacted = "redacted"
	secretReportFull     = "full"
)

func normalizeSecretReportMode(mode string, dangerousReveal bool) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", secretReportOff:
		return secretReportOff, nil
	case secretReportRedacted:
		return secretReportRedacted, nil
	case secretReportFull:
		if !dangerousReveal {
			return "", fmt.Errorf("--secret-report full requires --dangerously-reveal-secrets")
		}
		return secretReportFull, nil
	default:
		return "", fmt.Errorf("invalid --secret-report %q (want off, redacted, or full)", mode)
	}
}

func printSecretDiagnostics(w io.Writer, diagnostics []neurorouter.DetectedSecret, mode string) {
	if mode == secretReportFull {
		printDangerousSecretRevealWarning(w)
	}
	for _, diagnostic := range diagnostics {
		label := "preview"
		value := diagnostic.Value
		if mode == secretReportFull && diagnostic.FullValue != "" {
			label = "value"
			value = diagnostic.FullValue
		}
		_, _ = fmt.Fprintf(w, "      secret: type=%s line=%d %s=%s\n",
			diagnostic.Type, diagnostic.Line, label, value)
	}
}

func printDangerousSecretRevealWarning(w io.Writer) {
	_, _ = fmt.Fprintln(w, "      DANGER: FULL matched secret values below. Local debugging only; rotate anything exposed.")
}

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
)

func normalizeSecretReportMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", secretReportOff:
		return secretReportOff, nil
	case secretReportRedacted:
		return secretReportRedacted, nil
	default:
		return "", fmt.Errorf("invalid --secret-report %q (want off or redacted)", mode)
	}
}

func printSecretDiagnostics(w io.Writer, diagnostics []neurorouter.DetectedSecret) {
	for _, diagnostic := range diagnostics {
		_, _ = fmt.Fprintf(w, "      secret: type=%s line=%d preview=%s\n",
			diagnostic.Type, diagnostic.Line, diagnostic.Value)
	}
}

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ppiankov/neurorouter/internal/neurorouter"
)

func TestNormalizeSecretReportMode(t *testing.T) {
	for _, mode := range []string{"", "off", "redacted", "REDACTED"} {
		if _, err := normalizeSecretReportMode(mode); err != nil {
			t.Fatalf("normalize %q: %v", mode, err)
		}
	}

	if _, err := normalizeSecretReportMode("full"); err == nil {
		t.Fatal("expected invalid mode error for full")
	}
}

func TestPrintSecretDiagnostics(t *testing.T) {
	var out bytes.Buffer
	printSecretDiagnostics(&out, []neurorouter.DetectedSecret{{
		Type:  neurorouter.SecretOpenAIKey,
		Line:  3,
		Value: "sk-proj-...",
	}})

	got := out.String()
	if !strings.Contains(got, "type=openai_key") {
		t.Fatalf("missing type in output: %q", got)
	}
	if !strings.Contains(got, "line=3") {
		t.Fatalf("missing line in output: %q", got)
	}
	if !strings.Contains(got, "preview=sk-proj-...") {
		t.Fatalf("missing preview in output: %q", got)
	}
}

func TestAuditManagementURL(t *testing.T) {
	if got := auditManagementURL("localhost:4000", "", secretReportOff); got != "http://localhost:4000/v1/audit" {
		t.Fatalf("base audit url: got %q", got)
	}

	got := auditManagementURL("localhost:4000", "codex main", secretReportRedacted)
	if got != "http://localhost:4000/v1/audit?secret_report=redacted&session=codex+main" &&
		got != "http://localhost:4000/v1/audit?session=codex+main&secret_report=redacted" {
		t.Fatalf("audit redacted url: got %q", got)
	}
}

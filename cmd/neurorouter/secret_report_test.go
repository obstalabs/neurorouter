package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/obstalabs/neurorouter/internal/neurorouter"
)

func buildReportSecret(parts ...string) string {
	return strings.Join(parts, "")
}

func TestNormalizeSecretReportMode(t *testing.T) {
	for _, mode := range []string{"", "off", "redacted", "REDACTED"} {
		if _, err := normalizeSecretReportMode(mode, false); err != nil {
			t.Fatalf("normalize %q: %v", mode, err)
		}
	}

	if _, err := normalizeSecretReportMode("full", false); err == nil {
		t.Fatal("expected full mode without dangerous flag to fail")
	}
	if got, err := normalizeSecretReportMode("full", true); err != nil || got != secretReportFull {
		t.Fatalf("normalize full with dangerous flag: got %q err=%v", got, err)
	}
}

func TestPrintSecretDiagnostics(t *testing.T) {
	var out bytes.Buffer
	printSecretDiagnostics(&out, []neurorouter.DetectedSecret{{
		Type:  neurorouter.SecretOpenAIKey,
		Line:  3,
		Value: "sk-proj-...",
	}}, secretReportRedacted)

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

func TestPrintSecretDiagnosticsFull(t *testing.T) {
	var out bytes.Buffer
	fullValue := buildReportSecret("sk-proj-", "ABCDEFGHIJKLMN", "1234567890")
	printSecretDiagnostics(&out, []neurorouter.DetectedSecret{{
		Type:      neurorouter.SecretOpenAIKey,
		Line:      7,
		Value:     "sk-proj-...",
		FullValue: fullValue,
	}}, secretReportFull)

	got := out.String()
	if !strings.Contains(got, "DANGER: FULL matched secret values below") {
		t.Fatalf("missing danger warning: %q", got)
	}
	if !strings.Contains(got, "value="+fullValue) {
		t.Fatalf("missing full value in output: %q", got)
	}
}

func TestAuditManagementURL(t *testing.T) {
	if got := auditManagementURL("localhost:4000", "", secretReportOff, false); got != "http://localhost:4000/v1/audit" {
		t.Fatalf("base audit url: got %q", got)
	}

	got := auditManagementURL("localhost:4000", "codex main", secretReportRedacted, false)
	if got != "http://localhost:4000/v1/audit?secret_report=redacted&session=codex+main" &&
		got != "http://localhost:4000/v1/audit?session=codex+main&secret_report=redacted" {
		t.Fatalf("audit redacted url: got %q", got)
	}

	got = auditManagementURL("localhost:4000", "codex main", secretReportFull, true)
	if got != "http://localhost:4000/v1/audit?secret_report=full&dangerously_reveal_secrets=1&session=codex+main" &&
		got != "http://localhost:4000/v1/audit?secret_report=full&session=codex+main&dangerously_reveal_secrets=1" &&
		got != "http://localhost:4000/v1/audit?session=codex+main&secret_report=full&dangerously_reveal_secrets=1" &&
		got != "http://localhost:4000/v1/audit?session=codex+main&dangerously_reveal_secrets=1&secret_report=full" &&
		got != "http://localhost:4000/v1/audit?dangerously_reveal_secrets=1&session=codex+main&secret_report=full" &&
		got != "http://localhost:4000/v1/audit?dangerously_reveal_secrets=1&secret_report=full&session=codex+main" {
		t.Fatalf("audit full url: got %q", got)
	}
}

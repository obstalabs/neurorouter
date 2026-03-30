package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestRunDND_Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method: got %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/dnd" {
			t.Fatalf("path: got %s, want /v1/dnd", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"active":true,"manual":true,"source":"manual","status":"on (manual)"}`))
	}))
	defer server.Close()

	cmd := newDNDTestCommand()
	mustSetFlag(t, cmd, "addr", strings.TrimPrefix(server.URL, "http://"))

	if err := runDND(cmd, nil); err != nil {
		t.Fatalf("runDND: %v", err)
	}

	output := cmd.OutOrStdout().(*strings.Builder).String()
	if !strings.Contains(output, "DND: on (manual)") {
		t.Fatalf("output: %q", output)
	}
	if !strings.Contains(output, "Source: manual") {
		t.Fatalf("output: %q", output)
	}
	if !strings.Contains(output, "Active: true") {
		t.Fatalf("output: %q", output)
	}
}

func TestRunDND_Toggle(t *testing.T) {
	var seen []bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method: got %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/dnd" {
			t.Fatalf("path: got %s, want /v1/dnd", r.URL.Path)
		}

		var payload struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		seen = append(seen, payload.Enabled)

		status := "off"
		source := "off"
		if payload.Enabled {
			status = "on (manual)"
			source = "manual"
		}
		_, _ = w.Write([]byte(`{"active":` + boolString(payload.Enabled) + `,"manual":` + boolString(payload.Enabled) + `,"source":"` + source + `","status":"` + status + `"}`))
	}))
	defer server.Close()

	cmd := newDNDTestCommand()
	mustSetFlag(t, cmd, "addr", strings.TrimPrefix(server.URL, "http://"))

	if err := runDND(cmd, []string{"on"}); err != nil {
		t.Fatalf("runDND on: %v", err)
	}
	if err := runDND(cmd, []string{"off"}); err != nil {
		t.Fatalf("runDND off: %v", err)
	}

	if len(seen) != 2 || !seen[0] || seen[1] {
		t.Fatalf("toggle payloads: %+v", seen)
	}

	output := cmd.OutOrStdout().(*strings.Builder).String()
	if !strings.Contains(output, "DND enabled.") {
		t.Fatalf("output: %q", output)
	}
	if !strings.Contains(output, "DND disabled.") {
		t.Fatalf("output: %q", output)
	}
}

func newDNDTestCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "dnd"}
	cmd.SetOut(&strings.Builder{})
	dndCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		cmd.Flags().String(flag.Name, flag.DefValue, flag.Usage)
	})
	return cmd
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

package main

import "testing"

func TestManagementURL(t *testing.T) {
	if got := managementURL("localhost:4000", "/v1/audit", ""); got != "http://localhost:4000/v1/audit" {
		t.Fatalf("base url: got %q", got)
	}

	if got := managementURL("localhost:4000", "/v1/audit", "codex main"); got != "http://localhost:4000/v1/audit?session=codex+main" {
		t.Fatalf("session url: got %q", got)
	}
}

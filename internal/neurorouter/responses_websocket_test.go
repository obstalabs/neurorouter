package neurorouter

import (
	"testing"
	"time"
)

func TestResponsesWebsocketRegistry_ReusesExplicitSessionWithinIdleGrace(t *testing.T) {
	originalGrace := responsesWebsocketIdleGrace
	responsesWebsocketIdleGrace = time.Second
	defer func() { responsesWebsocketIdleGrace = originalGrace }()

	originalNow := timeNow
	now := time.Date(2026, time.April, 5, 12, 0, 0, 0, time.UTC)
	timeNow = func() time.Time { return now }
	defer func() { timeNow = originalNow }()

	registry := newResponsesWebsocketRegistry()
	first := registry.state("sess-explicit", "turn-1", false, true)
	if got := first.turnStateSnapshot(); got != "turn-1" {
		t.Fatalf("initial turn state: got %q, want %q", got, "turn-1")
	}
	registry.release("sess-explicit")

	now = now.Add(500 * time.Millisecond)
	second := registry.state("sess-explicit", "", false, true)
	if second != first {
		t.Fatal("expected explicit session state to be reused within idle grace")
	}
	registry.release("sess-explicit")

	now = now.Add(2 * time.Second)
	third := registry.state("sess-explicit", "", false, true)
	if third == first {
		t.Fatal("expected explicit session state to be pruned after idle grace")
	}
	registry.release("sess-explicit")
	registry.closeAll()
}

func TestResponsesWebsocketRegistry_ReleasesEphemeralSessionImmediately(t *testing.T) {
	registry := newResponsesWebsocketRegistry()

	first := registry.state("ws-ephemeral", "", true, true)
	registry.release("ws-ephemeral")
	if got := len(registry.states); got != 0 {
		t.Fatalf("states after ephemeral release: got %d, want 0", got)
	}

	second := registry.state("ws-ephemeral", "", true, true)
	if second == first {
		t.Fatal("expected ephemeral session state to be recreated after release")
	}
	registry.release("ws-ephemeral")
	registry.closeAll()
}

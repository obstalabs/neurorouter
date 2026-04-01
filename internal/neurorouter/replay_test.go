package neurorouter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type replayFixture struct {
	Name               string             `json:"name"`
	Description        string             `json:"description"`
	Capabilities       TargetCapabilities `json:"capabilities"`
	Filters            replayFilterConfig `json:"filters"`
	Request            json.RawMessage    `json:"request"`
	ExpectedFiltersRun []string           `json:"expected_filters_run"`
	ExpectedBody       json.RawMessage    `json:"expected_body"`
}

type replayFilterConfig struct {
	StaleReads      bool `json:"stale_reads"`
	Thinking        bool `json:"thinking"`
	OrphanedResults bool `json:"orphaned_results"`
	FailedRetries   bool `json:"failed_retries"`
	SystemReminders bool `json:"system_reminders"`
	OversizedBlocks bool `json:"oversized_blocks"`
}

func (c replayFilterConfig) filterConfig() FilterConfig {
	return FilterConfig{
		StaleReads:      c.StaleReads,
		Thinking:        c.Thinking,
		OrphanedResults: c.OrphanedResults,
		FailedRetries:   c.FailedRetries,
		SystemReminders: c.SystemReminders,
		OversizedBlocks: c.OversizedBlocks,
	}
}

func TestReplayFixtures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "replay", "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected replay fixtures")
	}

	for _, path := range files {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			fixture := loadReplayFixture(t, path)

			var req ResponsesRequest
			if err := json.Unmarshal(fixture.Request, &req); err != nil {
				t.Fatalf("decode request fixture: %v", err)
			}

			original, err := ExtractRequestMessages(&req)
			if err != nil {
				t.Fatalf("extract request messages: %v", err)
			}
			filtered := append([]ChatMessage(nil), original...)

			pipeline := NewPipeline(PipelineConfig{Filters: fixture.Filters.filterConfig()})
			if pipeline == nil {
				t.Fatal("expected pipeline for replay fixture")
			}

			adapter := SelectFilterAdapter(fixture.Capabilities, filtered)
			filtered, result, err := pipeline.Process(filtered, adapter)
			if err != nil {
				t.Fatalf("process pipeline: %v", err)
			}

			gotBody, extraFilters, err := replayExpectedBody(&req, fixture.Request, original, filtered, fixture.Capabilities, fixture.Filters.filterConfig())
			if err != nil {
				t.Fatalf("build replay output: %v", err)
			}
			combinedFilters := mergeFilterNames(result.FiltersRun, extraFilters)
			if !slices.Equal(combinedFilters, fixture.ExpectedFiltersRun) {
				t.Fatalf("filters_run: got %v, want %v", combinedFilters, fixture.ExpectedFiltersRun)
			}

			assertJSONEqual(t, gotBody, fixture.ExpectedBody)
		})
	}
}

func loadReplayFixture(t *testing.T, path string) replayFixture {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var fixture replayFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	return fixture
}

func replayExpectedBody(req *ResponsesRequest, rawBody json.RawMessage, original, filtered []ChatMessage, cap TargetCapabilities, cfg FilterConfig) ([]byte, []string, error) {
	if cap.WireAPI == WireAPIResponses {
		result, err := RewriteResponsesRequestWithConfig(rawBody, original, filtered, cfg)
		if err != nil {
			return nil, nil, err
		}
		return result.Body, mergeFilterNames(nil, result.FiltersRun), nil
	}

	chatReq, err := BuildChatRequest(req, filtered)
	if err != nil {
		return nil, nil, err
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, nil, err
	}
	return body, nil, nil
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()

	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got json: %v", err)
	}

	var wantValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode want json: %v", err)
	}

	gotJSON, err := json.Marshal(gotValue)
	if err != nil {
		t.Fatalf("marshal got json: %v", err)
	}
	wantJSON, err := json.Marshal(wantValue)
	if err != nil {
		t.Fatalf("marshal want json: %v", err)
	}

	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("json mismatch:\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

package neurorouter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFilterOversizedBlocks(t *testing.T) {
	f := FilterOversizedBlocks(100)

	t.Run("no truncation needed", func(t *testing.T) {
		msgs := []ChatMessage{{Role: "user", Content: "short message"}}
		out := f(msgs)
		if out[0].Content != "short message" {
			t.Errorf("unexpected content: %q", out[0].Content)
		}
	})

	t.Run("truncates large message", func(t *testing.T) {
		large := strings.Repeat("x", 200)
		msgs := []ChatMessage{{Role: "user", Content: large}}
		out := f(msgs)
		if len(out[0].Content) >= 200 {
			t.Errorf("expected truncation, got len %d", len(out[0].Content))
		}
		if !strings.Contains(out[0].Content, "[truncated by neurorouter") {
			t.Error("missing truncation marker")
		}
	})

	t.Run("preserves other messages", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "system", Content: "be helpful"},
			{Role: "user", Content: strings.Repeat("y", 200)},
			{Role: "assistant", Content: "ok"},
		}
		out := f(msgs)
		if out[0].Content != "be helpful" {
			t.Error("system message changed")
		}
		if out[2].Content != "ok" {
			t.Error("assistant message changed")
		}
		if !strings.Contains(out[1].Content, "[truncated") {
			t.Error("user message not truncated")
		}
	})

	t.Run("shapes claude bash tool_result json text", func(t *testing.T) {
		semanticFilter := FilterOversizedBlocks(160)
		resultJSON, err := json.Marshal(map[string]any{
			"stdout":                   strings.Repeat("a", 220),
			"stderr":                   "warning\n" + strings.Repeat("b", 120) + "\nexit status 7",
			"returnCodeInterpretation": "exit_code:7",
			"interrupted":              false,
		})
		if err != nil {
			t.Fatalf("marshal shell result: %v", err)
		}
		userContent, err := json.Marshal([]map[string]any{
			{
				"type":        "tool_result",
				"tool_use_id": "toolu_1",
				"content": []map[string]any{
					{"type": "text", "text": string(resultJSON)},
				},
				"is_error": true,
			},
		})
		if err != nil {
			t.Fatalf("marshal user content: %v", err)
		}

		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_1","name":"bash","input":{"command":"printf x"}}]`},
			{Role: "user", Content: string(userContent)},
		}

		out := semanticFilter(msgs)
		if len(out) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(out))
		}
		if !strings.Contains(out[1].Content, `"tool_use_id":"toolu_1"`) {
			t.Fatalf("tool result identity lost: %s", out[1].Content)
		}

		var blocks []map[string]any
		if err := json.Unmarshal([]byte(out[1].Content), &blocks); err != nil {
			t.Fatalf("decode shaped content: %v", err)
		}
		contentBlocks := blocks[0]["content"].([]any)
		text := contentBlocks[0].(map[string]any)["text"].(string)

		var shaped map[string]any
		if err := json.Unmarshal([]byte(text), &shaped); err != nil {
			t.Fatalf("decode shaped shell json: %v", err)
		}
		if !strings.Contains(shaped["stdout"].(string), "[truncated by neurorouter") {
			t.Fatalf("stdout was not shaped: %q", shaped["stdout"])
		}
		if !strings.Contains(shaped["stderr"].(string), "exit status 7") {
			t.Fatalf("stderr tail lost: %q", shaped["stderr"])
		}
		if shaped["returnCodeInterpretation"] != "exit_code:7" {
			t.Fatalf("returnCodeInterpretation lost: %#v", shaped)
		}
	})

	t.Run("shapes claude powershell tool_result json block", func(t *testing.T) {
		semanticFilter := FilterOversizedBlocks(160)
		userContent, err := json.Marshal([]map[string]any{
			{
				"type":        "tool_result",
				"tool_use_id": "toolu_2",
				"content": []map[string]any{
					{
						"type": "json",
						"value": map[string]any{
							"stdout":                   strings.Repeat("x", 220),
							"stderr":                   "",
							"returnCodeInterpretation": "exit_code:0",
							"backgroundedByUser":       true,
						},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("marshal user content: %v", err)
		}

		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_2","name":"PowerShell","input":{"command":"Write-Output hello"}}]`},
			{Role: "user", Content: string(userContent)},
		}

		out := semanticFilter(msgs)
		var blocks []map[string]any
		if err := json.Unmarshal([]byte(out[1].Content), &blocks); err != nil {
			t.Fatalf("decode shaped content: %v", err)
		}
		contentBlocks := blocks[0]["content"].([]any)
		value := contentBlocks[0].(map[string]any)["value"].(map[string]any)
		if !strings.Contains(value["stdout"].(string), "[truncated by neurorouter") {
			t.Fatalf("stdout was not shaped: %q", value["stdout"])
		}
		if value["backgroundedByUser"] != true {
			t.Fatalf("metadata lost: %#v", value)
		}
		if value["returnCodeInterpretation"] != "exit_code:0" {
			t.Fatalf("returnCodeInterpretation lost: %#v", value)
		}
	})

	t.Run("falls back safely for malformed shell json", func(t *testing.T) {
		semanticFilter := FilterOversizedBlocks(160)
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_3","name":"bash","input":{"command":"printf x"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_3","content":[{"type":"text","text":"not json at all"}]}]`},
		}

		out := semanticFilter(msgs)
		if len(out) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(out))
		}
		if out[1].Content == "" {
			t.Fatal("expected malformed shell result to remain")
		}
	})

	t.Run("shapes claude read tool_result while preserving tool identity", func(t *testing.T) {
		semanticFilter := FilterOversizedBlocks(16 * 1024)
		large := strings.Repeat("z", 6*1024)
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_4","name":"Read","input":{"file_path":"/tmp/x"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_4","content":"` + large + `"}]`},
		}

		out := semanticFilter(msgs)
		if !strings.Contains(out[1].Content, `"tool_use_id":"toolu_4"`) {
			t.Fatalf("expected tool_use_id to survive shaping, got %s", out[1].Content)
		}
		if !strings.Contains(out[1].Content, "[truncated by neurorouter") {
			t.Fatalf("expected semantic read shaping, got %s", out[1].Content)
		}
	})
}

func TestFilterThinking(t *testing.T) {
	t.Run("removes thinking blocks", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: "<thinking>internal reasoning</thinking>Here is my answer."},
		}
		out := FilterThinking(msgs)
		if len(out) != 1 {
			t.Fatalf("expected 1 message, got %d", len(out))
		}
		if strings.Contains(out[0].Content, "thinking") {
			t.Errorf("thinking block not removed: %q", out[0].Content)
		}
		if out[0].Content != "Here is my answer." {
			t.Errorf("unexpected content: %q", out[0].Content)
		}
	})

	t.Run("drops empty messages", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: "<thinking>only thinking</thinking>"},
			{Role: "user", Content: "hello"},
		}
		out := FilterThinking(msgs)
		if len(out) != 1 {
			t.Fatalf("expected 1 message, got %d", len(out))
		}
		if out[0].Role != "user" {
			t.Errorf("wrong message kept: %q", out[0].Role)
		}
	})

	t.Run("preserves valid claude block arrays with tool_use blocks", func(t *testing.T) {
		msgs := []ChatMessage{
			{
				Role: "assistant",
				Content: `[{"type":"thinking","thinking":"internal reasoning"},` +
					`{"type":"text","text":"Let me check cleanlive.go and find renameOrCopy."},` +
					`{"type":"tool_use","id":"toolu_grep","name":"Grep","input":{"path":"/repo","pattern":"renameOrCopy","output_mode":"content"}},` +
					`{"type":"tool_use","id":"toolu_read_old","name":"Read","input":{"file_path":"/repo/internal/editor/cleanlive.go","offset":110,"limit":30}}]`,
			},
		}

		out := FilterThinking(msgs)
		if len(out) != 1 {
			t.Fatalf("expected 1 message, got %d", len(out))
		}
		if strings.Contains(out[0].Content, `"type":"thinking"`) {
			t.Fatalf("thinking block should be removed: %s", out[0].Content)
		}
		if !strings.Contains(out[0].Content, "toolu_grep") || !strings.Contains(out[0].Content, "toolu_read_old") {
			t.Fatalf("tool_use blocks should be preserved: %s", out[0].Content)
		}
		if !json.Valid([]byte(out[0].Content)) {
			t.Fatalf("Claude block array should remain valid JSON: %s", out[0].Content)
		}
	})
}

func TestFilterSystemReminders(t *testing.T) {
	reminder := "<system-reminder>Remember to be helpful</system-reminder>"

	t.Run("deduplicates reminders", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "user", Content: "hello " + reminder},
			{Role: "user", Content: "world " + reminder},
			{Role: "user", Content: "last " + reminder},
		}
		out := FilterSystemReminders(msgs)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(out))
		}
		// First two should have reminder removed.
		if strings.Contains(out[0].Content, "system-reminder") {
			t.Error("first message still has reminder")
		}
		if strings.Contains(out[1].Content, "system-reminder") {
			t.Error("second message still has reminder")
		}
		// Last should keep it.
		if !strings.Contains(out[2].Content, "system-reminder") {
			t.Error("last message should keep reminder")
		}
	})

	t.Run("no-op without duplicates", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "user", Content: "hello " + reminder},
		}
		out := FilterSystemReminders(msgs)
		if len(out) != 1 || out[0].Content != msgs[0].Content {
			t.Error("single reminder should not be removed")
		}
	})

	t.Run("deduplicates repeated claude continuation summaries", func(t *testing.T) {
		summaryBodyA := strings.Repeat("Summary A line\n", 24)
		summaryBodyB := strings.Repeat("Summary B line\n", 24)
		summaryA := `[{"type":"text","text":"This session is being continued from a previous conversation.\n[coalesced]\n` + summaryBodyA + `"}]`
		summaryB := `[{"type":"text","text":"This session is being continued from a previous conversation.\n[coalesced]\n` + summaryBodyB + `"}]`
		msgs := []ChatMessage{
			{Role: "assistant", Content: summaryA},
			{Role: "assistant", Content: summaryA},
			{Role: "assistant", Content: summaryB},
			{Role: "user", Content: "Continue."},
		}

		out := FilterSystemReminders(msgs)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages after duplicate summary removal, got %d", len(out))
		}
		if out[0].Content != summaryA {
			t.Fatalf("expected latest duplicate summary to remain first, got %q", out[0].Content)
		}
		if out[1].Content != summaryB {
			t.Fatalf("expected distinct summary to remain, got %q", out[1].Content)
		}
		if out[2].Content != "Continue." {
			t.Fatalf("expected trailing user message to remain, got %q", out[2].Content)
		}
	})
}

func TestFilterStaleReads(t *testing.T) {
	t.Run("removes stale reads", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `{"name":"Read","id":"t1","input":{"file_path":"/foo/bar.go"}}`},
			{Role: "user", Content: "file contents here"},
			{Role: "assistant", Content: `some other work`},
			{Role: "assistant", Content: `{"name":"Read","id":"t2","input":{"file_path":"/foo/bar.go"}}`},
			{Role: "user", Content: "file contents again"},
		}
		out := FilterStaleReads(msgs)
		// First read (index 0) is stale — same file read again at index 3 with no write between.
		if len(out) != 4 {
			t.Fatalf("expected 4 messages (1 stale read dropped), got %d", len(out))
		}
		// The kept messages should be: user, other work, second read, second result.
		if strings.Contains(out[0].Content, `"name":"Read"`) {
			t.Error("first read should have been dropped")
		}
	})

	t.Run("keeps reads with intervening write", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `{"name":"Read","id":"t1","input":{"file_path":"/foo/bar.go"}}`},
			{Role: "assistant", Content: `{"name":"Write","id":"t2","input":{"file_path":"/foo/bar.go"}}`},
			{Role: "assistant", Content: `{"name":"Read","id":"t3","input":{"file_path":"/foo/bar.go"}}`},
		}
		out := FilterStaleReads(msgs)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages (write intervened), got %d", len(out))
		}
	})

	t.Run("deduplicates only identical reads within each write-delimited segment", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `{"name":"Read","id":"t1","input":{"file_path":"/foo/bar.go"}}`},
			{Role: "assistant", Content: `{"name":"Read","id":"t2","input":{"file_path":"/foo/bar.go","offset":100,"limit":50}}`},
			{Role: "assistant", Content: `{"name":"Read","id":"t3","input":{"file_path":"/foo/bar.go"}}`},
			{Role: "assistant", Content: `{"name":"Write","id":"t4","input":{"file_path":"/foo/bar.go"}}`},
			{Role: "assistant", Content: `{"name":"Read","id":"t5","input":{"file_path":"/foo/bar.go","offset":200,"limit":20}}`},
		}

		out := FilterStaleReads(msgs)
		if len(out) != 4 {
			t.Fatalf("expected only the identical pre-write duplicate removed, got %d messages", len(out))
		}
		if strings.Contains(out[0].Content, `"id":"t1"`) {
			t.Fatalf("earlier identical read in the same segment should be removed: %#v", out)
		}
		if !strings.Contains(out[0].Content, `"id":"t2"`) {
			t.Fatalf("distinct read before the write should remain: %#v", out)
		}
		if !strings.Contains(out[1].Content, `"id":"t3"`) {
			t.Fatalf("latest duplicate before the write should remain: %#v", out)
		}
		if !strings.Contains(out[3].Content, `"id":"t5"`) {
			t.Fatalf("post-write distinct read should remain: %#v", out)
		}
	})

	t.Run("preserves distinct claude read blocks in mixed tool turns", func(t *testing.T) {
		msgs := []ChatMessage{
			{
				Role: "assistant",
				Content: `[{"type":"text","text":"Let me check cleanlive.go and find renameOrCopy."},` +
					`{"type":"tool_use","id":"toolu_grep","name":"Grep","input":{"path":"/repo","pattern":"renameOrCopy","output_mode":"content"}},` +
					`{"type":"tool_use","id":"toolu_read_old","name":"Read","input":{"file_path":"/repo/internal/editor/cleanlive.go","offset":110,"limit":30}}]`,
			},
			{
				Role: "user",
				Content: `[{"type":"tool_result","tool_use_id":"toolu_grep","content":"internal/editor/rename.go:13:func renameOrCopy(src, dst string) error {"},` +
					`{"type":"tool_result","tool_use_id":"toolu_read_old","content":"old cleanlive snippet"}]`,
			},
			{
				Role:    "assistant",
				Content: `[{"type":"tool_use","id":"toolu_read_new","name":"Read","input":{"file_path":"/repo/internal/editor/cleanlive.go","offset":295,"limit":20}}]`,
			},
			{
				Role:    "user",
				Content: `[{"type":"tool_result","tool_use_id":"toolu_read_new","content":"new cleanlive snippet"}]`,
			},
		}

		out := FilterStaleReads(msgs)
		if len(out) != 4 {
			t.Fatalf("expected mixed turn to remain intact, got %d messages", len(out))
		}
		if !strings.Contains(out[0].Content, "toolu_read_old") {
			t.Fatalf("distinct earlier read should remain in assistant turn: %s", out[0].Content)
		}
		if !strings.Contains(out[0].Content, "toolu_grep") {
			t.Fatalf("live grep tool_use should remain in assistant turn: %s", out[0].Content)
		}
		if !strings.Contains(out[1].Content, "toolu_read_old") {
			t.Fatalf("distinct earlier read result should remain in user turn: %s", out[1].Content)
		}
		if !strings.Contains(out[1].Content, "toolu_grep") {
			t.Fatalf("live grep tool_result should remain in user turn: %s", out[1].Content)
		}
	})

	t.Run("preserves distinct claude read pairs for the same file", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_read_old","name":"Read","input":{"file_path":"/repo/internal/editor/cleanlive.go","offset":110,"limit":30}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_read_old","content":"old cleanlive snippet"}]`},
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_read_new","name":"Read","input":{"file_path":"/repo/internal/editor/cleanlive.go","offset":295,"limit":20}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_read_new","content":"new cleanlive snippet"}]`},
		}

		out := FilterStaleReads(msgs)
		if len(out) != 4 {
			t.Fatalf("expected both distinct read pairs to remain, got %d messages", len(out))
		}
		if !strings.Contains(out[0].Content, "toolu_read_old") || !strings.Contains(out[1].Content, "toolu_read_old") {
			t.Fatalf("distinct earlier Claude read pair should remain: %#v", out)
		}
	})

	t.Run("drops duplicate claude read pairs for the same file segment", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_read_old","name":"Read","input":{"file_path":"/repo/internal/editor/cleanlive.go","offset":110,"limit":30}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_read_old","content":"old cleanlive snippet"}]`},
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_read_new","name":"Read","input":{"file_path":"/repo/internal/editor/cleanlive.go","offset":110,"limit":30}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_read_new","content":"new cleanlive snippet"}]`},
		}

		out := FilterStaleReads(msgs)
		if len(out) != 2 {
			t.Fatalf("expected only the latest duplicate read pair to remain, got %d messages", len(out))
		}
		if strings.Contains(out[0].Content, "toolu_read_old") || strings.Contains(out[1].Content, "toolu_read_old") {
			t.Fatalf("duplicate Claude read pair should be removed: %#v", out)
		}
	})

	t.Run("drops duplicate claude shell transcript chains", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_shell_old","name":"bash","input":{"command":"git status"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_shell_old","content":[{"type":"json","value":{"stdout":"On branch main\nnothing to commit\n","stderr":"","returnCodeInterpretation":"exit_code:0"}}]}]`},
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_shell_new","name":"bash","input":{"command":"git status"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_shell_new","content":[{"type":"json","value":{"stdout":"On branch main\nnothing to commit\n","stderr":"","returnCodeInterpretation":"exit_code:0"}}]}]`},
			{Role: "user", Content: "Continue."},
		}

		out := FilterStaleReads(msgs)
		if len(out) != 3 {
			t.Fatalf("expected duplicate shell pair removed, got %d messages", len(out))
		}
		if strings.Contains(out[0].Content, "toolu_shell_old") || strings.Contains(out[1].Content, "toolu_shell_old") {
			t.Fatalf("expected earlier shell transcript chain removed: %#v", out)
		}
		if !strings.Contains(out[0].Content, "toolu_shell_new") || !strings.Contains(out[1].Content, "toolu_shell_new") {
			t.Fatalf("expected latest shell transcript chain to remain: %#v", out)
		}
	})

	t.Run("keeps distinct claude shell transcript chains", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_shell_old","name":"bash","input":{"command":"git status"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_shell_old","content":[{"type":"json","value":{"stdout":"On branch main\nnothing to commit\n","stderr":"","returnCodeInterpretation":"exit_code:0"}}]}]`},
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_shell_new","name":"bash","input":{"command":"git status"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_shell_new","content":[{"type":"json","value":{"stdout":"On branch feature\nmodified: README.md\n","stderr":"","returnCodeInterpretation":"exit_code:0"}}]}]`},
		}

		out := FilterStaleReads(msgs)
		if len(out) != 4 {
			t.Fatalf("expected distinct shell transcript chains to remain, got %d messages", len(out))
		}
	})
}

func TestFilterOrphanedResults(t *testing.T) {
	t.Run("removes orphaned tool_result", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_123","name":"Read"}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_123","content":"ok"}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_999","content":"orphaned"}]`},
		}
		out := FilterOrphanedResults(msgs)
		if len(out) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(out))
		}
	})

	t.Run("keeps all when no orphans", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_1","name":"Bash"}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_1","content":"done"}]`},
		}
		out := FilterOrphanedResults(msgs)
		if len(out) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(out))
		}
	})
}

func TestFilterFailedRetries(t *testing.T) {
	t.Run("removes failed+error pair when retried", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/x"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_1","content":"error: no such file"}]`},
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_2","name":"Read","input":{"file_path":"/y"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_2","content":"file contents"}]`},
		}
		out := FilterFailedRetries(msgs)
		if len(out) != 2 {
			t.Fatalf("expected 2 messages (failed pair dropped), got %d", len(out))
		}
	})

	t.Run("keeps non-retried errors", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_1","content":"error: command not found"}]`},
			{Role: "assistant", Content: "I see the command failed. Let me try a different approach."},
		}
		out := FilterFailedRetries(msgs)
		// No retry with same tool name, so nothing dropped.
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(out))
		}
	})

	t.Run("drops superseded claude bash retries after success", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_bash_old","name":"bash","input":{"command":"go test ./..."}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_bash_old","content":[{"type":"json","value":{"stdout":"FAIL\t./...\n","stderr":"exit status 1","returnCodeInterpretation":"exit_code:1"}}]}]`},
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_bash_new","name":"bash","input":{"command":"go test ./..."}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_bash_new","content":[{"type":"json","value":{"stdout":"ok\t./...\n","stderr":"","returnCodeInterpretation":"exit_code:0"}}]}]`},
			{Role: "user", Content: "Continue."},
		}

		out := FilterFailedRetries(msgs)
		if len(out) != 3 {
			t.Fatalf("expected failed bash retry pair removed, got %d messages", len(out))
		}
		if strings.Contains(out[0].Content, "toolu_bash_old") || strings.Contains(out[1].Content, "toolu_bash_old") {
			t.Fatalf("expected stale bash retry removed: %#v", out)
		}
	})

	t.Run("drops superseded claude powershell retries after success", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_ps_old","name":"PowerShell","input":{"command":"Get-ChildItem"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_ps_old","content":[{"type":"json","value":{"stdout":"","stderr":"Access denied","exitCode":1}}]}]`},
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_ps_new","name":"PowerShell","input":{"command":"Get-ChildItem"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_ps_new","content":[{"type":"json","value":{"stdout":"foo.txt\n","stderr":"","exitCode":0}}]}]`},
		}

		out := FilterFailedRetries(msgs)
		if len(out) != 2 {
			t.Fatalf("expected failed powershell retry pair removed, got %d messages", len(out))
		}
		if strings.Contains(out[0].Content, "toolu_ps_old") || strings.Contains(out[1].Content, "toolu_ps_old") {
			t.Fatalf("expected stale PowerShell retry removed: %#v", out)
		}
	})

	t.Run("keeps unresolved claude shell failures", func(t *testing.T) {
		msgs := []ChatMessage{
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_bash_old","name":"bash","input":{"command":"go test ./..."}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_bash_old","content":[{"type":"json","value":{"stdout":"FAIL\t./...\n","stderr":"exit status 1","returnCodeInterpretation":"exit_code:1"}}]}]`},
			{Role: "assistant", Content: `[{"type":"tool_use","id":"toolu_other","name":"bash","input":{"command":"git status"}}]`},
			{Role: "user", Content: `[{"type":"tool_result","tool_use_id":"toolu_other","content":[{"type":"json","value":{"stdout":"On branch main\n","stderr":"","returnCodeInterpretation":"exit_code:0"}}]}]`},
		}

		out := FilterFailedRetries(msgs)
		if len(out) != 4 {
			t.Fatalf("expected unresolved failure to remain, got %d messages", len(out))
		}
	})
}

func TestFilterChain_Composition(t *testing.T) {
	cfg := FilterConfig{
		OversizedBlocks: true,
		Thinking:        true,
		MaxBlockBytes:   50,
	}
	chain := NewFilterChain(cfg)
	if chain == nil {
		t.Fatal("expected non-nil chain")
	}

	msgs := []ChatMessage{
		{Role: "assistant", Content: "<thinking>hmm</thinking>answer"},
		{Role: "user", Content: strings.Repeat("x", 100)},
	}

	out, result := chain.Run(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if strings.Contains(out[0].Content, "thinking") {
		t.Error("thinking not removed")
	}
	if !strings.Contains(out[1].Content, "[truncated") {
		t.Error("oversized not truncated")
	}
	if result.BytesBefore <= result.BytesAfter {
		t.Error("expected bytes reduction")
	}
	if len(result.Applied) == 0 {
		t.Error("expected filters to be listed as applied")
	}
}

func TestFilterChain_Nil(t *testing.T) {
	chain := NewFilterChain(FilterConfig{})
	if chain != nil {
		t.Error("expected nil chain when no filters enabled")
	}
}

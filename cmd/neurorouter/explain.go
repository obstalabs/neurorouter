package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var explainCmd = &cobra.Command{
	Use:   "explain <pattern>",
	Short: "Explain what a detected pattern means and how to fix it",
	Args:  cobra.ExactArgs(1),
	RunE:  runExplain,
}

var explanations = map[string]struct {
	title       string
	description string
	fix         string
	savings     string
}{
	"stale_reads": {
		title:       "stale_reads — repeated file reads without intervening writes",
		description: "Your session reads the same file multiple times. Each read consumes tokens but provides no new information after the first read.",
		fix:         "cache file contents in your client workflow or compact after the first full read.\n  → review repeated reads with neurorouter audit or stats",
		savings:     "~4KB per duplicate read (~$0.003 per read at Sonnet pricing)",
	},
	"thinking_bloat": {
		title:       "thinking_bloat — thinking blocks consuming significant token budget",
		description: "Extended thinking blocks (<thinking>...</thinking>) are included in the context but provide no value to downstream processing. They can consume 10-30% of token spend.",
		fix:         "enable the thinking filter to strip thinking blocks automatically.\n  → neurorouter proxy --no-filter=false (enabled by default)",
		savings:     "10-30% cost reduction on thinking-heavy sessions",
	},
	"reminder_spam": {
		title:       "reminder_spam — duplicate system reminders across messages",
		description: "The same <system-reminder> blocks appear in multiple messages. Only the last occurrence matters; earlier duplicates waste tokens.",
		fix:         "enable the system_reminders filter to deduplicate automatically.\n  → neurorouter proxy --no-filter=false (enabled by default)",
		savings:     "varies by reminder size, typically 2-5KB per duplicate",
	},
	"context_bloat": {
		title:       "context_bloat — high percentage of context is noise",
		description: "More than 25% of your request content is being filtered out as noise. This means you're paying for tokens that carry no signal.",
		fix:         "keep filters enabled and compact earlier in the client session.\n  → use your client's compact or summarize flow before the context snowballs",
		savings:     "proportional to noise percentage, often $0.05-0.50 per request",
	},
	"request_repeat": {
		title:       "request_repeat — identical prompts sent multiple times",
		description: "The same user message (>100 chars) was sent 3+ times. This could indicate a retry loop, stale cache, or repeated manual action.",
		fix:         "cache repeated queries or stop the retry loop at the client/tool layer.\n  → inspect recent repeats with neurorouter stats or audit",
		savings:     "full cost of each duplicate request",
	},
	"large_tool_output": {
		title:       "large_tool_output — tool results exceeding 10KB",
		description: "Multiple tool outputs exceed 10KB. Large outputs consume significant context window and token budget without proportional value.",
		fix:         "trim large tool output before it reaches the model.\n  → prefer narrower file reads, shorter command output, or smaller diffs in the client workflow",
		savings:     "proportional to output size, often significant for file reads and command outputs",
	},
}

func runExplain(cmd *cobra.Command, args []string) error {
	pattern := strings.ToLower(args[0])

	e, ok := explanations[pattern]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown pattern: %s\n\nAvailable patterns:\n", pattern)
		for name := range explanations {
			fmt.Fprintf(os.Stderr, "  %s\n", name)
		}
		return fmt.Errorf("unknown pattern %q", pattern)
	}

	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n%s\n\nFix: %s\n\nEstimated savings: %s\n",
		e.title, e.description, e.fix, e.savings)
	return err
}

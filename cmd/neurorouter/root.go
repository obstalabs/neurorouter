package main

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "neurorouter",
	Short: "Context hygiene layer for local AI coding sessions",
	Long: `NeuroRouter is a local context hygiene layer that shapes AI requests before they leave your machine.

  protect: catch leaked secrets before they reach the API
  shape:   remove obvious stale context, retries, reminders, and oversized blocks
  preserve: keep supported client protocol semantics intact
  observe: show local audit evidence for what changed

Start the proxy:
  neurorouter proxy
  neurorouter proxy --target https://api.openai.com

Or just run with auto-detection:
  export ANTHROPIC_API_KEY=...
  neurorouter proxy`,
	// No subcommand = start proxy (the 30-second path).
	RunE: proxyCmd.RunE,
}

func init() {
	// Inherit proxy flags on root so `neurorouter --listen :5000` works.
	rootCmd.Flags().AddFlagSet(proxyCmd.Flags())

	rootCmd.AddCommand(proxyCmd)
	rootCmd.AddCommand(statsCmd)
	rootCmd.AddCommand(explainCmd)
	rootCmd.AddCommand(auditCmd)
	rootCmd.AddCommand(dndCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(versionCmd)
}

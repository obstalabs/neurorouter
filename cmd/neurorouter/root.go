package main

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "neurorouter",
	Short: "LLM proxy that filters noise and catches secrets before they leave your machine",
	Long: `NeuroRouter is a local LLM proxy that makes your requests better before they leave your machine.

  protect: catch leaked secrets before they reach the API
  purify:  strip noise, duplicates, and waste from prompts
  route:   send requests to the right model
  observe: detect patterns and suggest improvements

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

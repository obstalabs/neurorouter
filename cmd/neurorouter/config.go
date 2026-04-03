package main

import (
	"fmt"

	"github.com/obstalabs/neurorouter/internal/neurorouter"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
	Long:  "View and modify ~/.neurorouter/config.toml settings.",
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show all settings with current values",
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := neurorouter.LoadConfig("")
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()

		if _, err := fmt.Fprintf(out, "%-22s %-12s %-12s %s\n", "KEY", "VALUE", "DEFAULT", "DESCRIPTION"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "%-22s %-12s %-12s %s\n", "---", "-----", "-------", "-----------"); err != nil {
			return err
		}

		for _, k := range neurorouter.ConfigKeys {
			val, _ := neurorouter.GetConfigValue(cfg, k.Name)
			if val == "" {
				val = "(unset)"
			}
			if _, err := fmt.Fprintf(out, "%-22s %-12s %-12s %s\n", k.Name, val, k.Default, k.Desc); err != nil {
				return err
			}
		}
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a config value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := neurorouter.LoadConfig("")
		if err != nil {
			return err
		}
		val, err := neurorouter.GetConfigValue(cfg, args[0])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), val)
		return err
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := neurorouter.SetConfigValue("", args[0], args[1]); err != nil {
			return err
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", args[0], args[1])
		return err
	},
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create default config file with commented defaults",
	RunE: func(cmd *cobra.Command, _ []string) error {
		path := neurorouter.DefaultConfigPath()
		if err := neurorouter.InitDefaultConfig(path); err != nil {
			return err
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "Config created at %s\n", path)
		return err
	},
}

func init() {
	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configInitCmd)
}

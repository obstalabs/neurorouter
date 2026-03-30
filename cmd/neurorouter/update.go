package main

import (
	"fmt"
	"os"

	"github.com/ppiankov/neurorouter/internal/neurorouter"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check for and install updates",
	Long:  "Checks GitHub releases for a newer version and downloads the platform-specific binary.",
	RunE:  runUpdate,
}

func init() {
	updateCmd.Flags().Bool("check", false, "only check if update is available, don't download")
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	checkOnly, _ := cmd.Flags().GetBool("check")

	fmt.Fprintf(os.Stderr, "current version: %s\n", version)
	fmt.Fprintf(os.Stderr, "checking %s for updates...\n", neurorouter.UpdateRepo)

	release, err := neurorouter.CheckUpdate(version)
	if err != nil {
		return fmt.Errorf("check failed: %w", err)
	}

	if release == nil {
		fmt.Fprintln(os.Stderr, "already up to date.")
		return nil
	}

	fmt.Fprintf(os.Stderr, "update available: %s\n", release.TagName)

	if checkOnly {
		return nil
	}

	assetURL, err := neurorouter.FindAssetURL(release)
	if err != nil {
		return err
	}

	checksumURL := neurorouter.FindChecksumURL(release)
	assetName := neurorouter.AssetName(release.TagName)

	fmt.Fprintf(os.Stderr, "downloading %s...\n", assetName)

	archive, err := neurorouter.DownloadUpdate(assetURL, checksumURL, assetName)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer func() { _ = os.Remove(archive.Path) }()

	fmt.Fprintln(os.Stderr, "extracting verified archive...")

	binaryPath, err := neurorouter.ExtractBinary(archive.Path, archive.AssetName)
	if err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}
	defer func() { _ = os.Remove(binaryPath) }()

	fmt.Fprintln(os.Stderr, "installing extracted binary...")

	if err := neurorouter.ReplaceBinary(binaryPath); err != nil {
		return fmt.Errorf("install failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "updated to %s\n", release.TagName)
	return nil
}

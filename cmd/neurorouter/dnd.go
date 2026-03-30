package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

var dndCmd = &cobra.Command{
	Use:   "dnd [on|off]",
	Short: "Toggle do-not-disturb mode on the running proxy",
	Long:  "When DND is active, the proxy suppresses non-critical suggestions. Only critical safety signals break through.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runDND,
}

func init() {
	dndCmd.Flags().String("addr", "localhost:4000", "proxy address to query")
	dndCmd.Flags().String("session", "", "session identifier to inspect or control")
}

func runDND(cmd *cobra.Command, args []string) error {
	addr, _ := cmd.Flags().GetString("addr")
	session, _ := cmd.Flags().GetString("session")
	out := cmd.OutOrStdout()

	if len(args) == 0 {
		status, err := fetchDNDStatus(addr, session)
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintf(out, "DND: %s\n", status.Status); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "Source: %s\n", status.Source); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Active: %t\n", status.Active)
		return err
	}

	var enabled bool
	switch args[0] {
	case "on":
		enabled = true
	case "off":
		enabled = false
	default:
		return fmt.Errorf("invalid argument %q: use 'on' or 'off'", args[0])
	}

	status, err := updateDNDStatus(addr, session, enabled)
	if err != nil {
		return err
	}

	if enabled {
		if _, err := fmt.Fprintln(out, "DND enabled."); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(out, "DND disabled."); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(out, "DND: %s\n", status.Status)
	return err
}

type dndStatusResponse struct {
	Active bool   `json:"active"`
	Manual bool   `json:"manual"`
	Source string `json:"source"`
	Status string `json:"status"`
}

func fetchDNDStatus(addr, session string) (dndStatusResponse, error) {
	resp, err := http.Get(managementURL(addr, "/v1/dnd", session))
	if err != nil {
		return dndStatusResponse{}, fmt.Errorf("connect to proxy at %s: %w", addr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeDNDResponse(addr, resp)
}

func updateDNDStatus(addr, session string, enabled bool) (dndStatusResponse, error) {
	body, err := json.Marshal(map[string]bool{"enabled": enabled})
	if err != nil {
		return dndStatusResponse{}, fmt.Errorf("encode dnd body: %w", err)
	}

	resp, err := http.Post(managementURL(addr, "/v1/dnd", session), "application/json", bytes.NewReader(body))
	if err != nil {
		return dndStatusResponse{}, fmt.Errorf("connect to proxy at %s: %w", addr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeDNDResponse(addr, resp)
}

func decodeDNDResponse(addr string, resp *http.Response) (dndStatusResponse, error) {
	if resp.StatusCode != http.StatusOK {
		return dndStatusResponse{}, fmt.Errorf("dnd endpoint unavailable at %s (status %d): use the default loopback bind or start the proxy with --public --expose-management", addr, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return dndStatusResponse{}, fmt.Errorf("read dnd response: %w", err)
	}

	var status dndStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return dndStatusResponse{}, fmt.Errorf("decode dnd response: %w", err)
	}
	return status, nil
}

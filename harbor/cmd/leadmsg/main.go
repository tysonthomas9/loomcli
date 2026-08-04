// leadmsg: minimal cross-process interface to a controlled lead runtime.
// POC/harness tool for the persistent-lead benchmark arm — calls the exact
// delivery function the driver outbox and web UI use (leadcontrol.
// DeliverLeadMessageWithOptions), with none of the DriverRun gating.
// Also evidence for the missing product surface (`loom agentctl message`).
//
// Usage:
//
//	leadmsg <workspace-key> <agent-name> <message>   deliver (or queue) a message
//	leadmsg <workspace-key> <agent-name> --status    read runtime metadata only
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func main() {
	if os.Getenv("LEADMSG_DEBUG") != "" {
		// Surface Debug-level internals (e.g. the embedded-runtime reuse
		// rejection reason, which openstore logs only at Debug).
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr,
			&slog.HandlerOptions{Level: slog.LevelDebug})))
	}
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: leadmsg <workspace-key> <agent-name> (<message>|--status)")
		os.Exit(2)
	}
	ws, agent, arg := os.Args[1], os.Args[2], os.Args[3]
	err := cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		if arg == "--status" {
			session, err := store.OrchestrationSessionFor(ctx, h.Store, ws, agent)
			if err != nil {
				return err
			}
			if session == nil {
				fmt.Println(`{"Status":"no-session"}`)
				return nil
			}
			rt := leadcontrol.RuntimeMetadataFromSession(session)
			out, _ := json.MarshalIndent(map[string]any{
				"SessionID":  session.SessionID,
				"Status":     rt.Status,
				"Endpoint":   rt.Endpoint,
				"ThreadID":   rt.ThreadID,
				"Controlled": rt.Controlled,
			}, "", "  ")
			fmt.Println(string(out))
			return nil
		}
		res, err := leadcontrol.DeliverLeadMessageWithOptions(ctx, h.Store, ws, agent, arg,
			leadcontrol.LeadMessageDeliveryOptions{SourceKind: "harness"})
		if err != nil {
			return err
		}
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "leadmsg error:", err)
		os.Exit(1)
	}
}

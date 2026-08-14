package terminal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestTerminalNotificationsCoverEveryDeliveryMutation(t *testing.T) {
	tests := []struct {
		name string
		run  func(interaction.TerminalTabs) error
		want []string
	}{
		{
			name: "patch with issue reassignment",
			run: func(svc interaction.TerminalTabs) error {
				_, err := svc.PatchTab(t.Context(), "WS", "tab-1", map[string]string{"issue_id": "ISSUE-1"})
				return err
			},
			want: []string{"terminal_metadata", "terminal_session_change"},
		},
		{
			name: "put",
			run: func(svc interaction.TerminalTabs) error {
				_, err := svc.PutTab(t.Context(), interaction.PutTerminalTabCommand{
					WorkspaceKey: "WS", TerminalID: "tab-1", Label: "Tab", Backend: "shell",
				})
				return err
			},
			want: []string{"terminal_metadata"},
		},
		{
			name: "delete",
			run: func(svc interaction.TerminalTabs) error {
				return svc.DeleteTab(t.Context(), "WS", "tab-1")
			},
			want: []string{"terminal_metadata"},
		},
		{
			name: "setup",
			run: func(svc interaction.TerminalTabs) error {
				_, err := svc.StartSetup(t.Context(), "WS", interaction.TerminalSetupRequest{})
				return err
			},
			want: []string{"terminal_metadata"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub, client := notificationTestHub(t)
			svc := WithTerminalNotifications(&notificationTerminalTabs{}, hub)
			if err := tt.run(svc); err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				select {
				case got := <-client.Send():
					if got.Type != want || got.WorkspaceID != "WS" || got.EntityID != "tab-1" {
						t.Fatalf("mutation = %#v, want type %q for WS/tab-1", got, want)
					}
				case <-time.After(time.Second):
					t.Fatalf("timed out waiting for %q", want)
				}
			}
		})
	}
}

func TestTerminalNotificationsDoNotPublishFailedMutations(t *testing.T) {
	hub, client := notificationTestHub(t)
	svc := WithTerminalNotifications(&notificationTerminalTabs{err: errors.New("write failed")}, hub)
	if err := svc.DeleteTab(t.Context(), "WS", "tab-1"); err == nil {
		t.Fatal("expected delete failure")
	}
	select {
	case got := <-client.Send():
		t.Fatalf("unexpected mutation after failure: %#v", got)
	case <-time.After(25 * time.Millisecond):
	}
}

func notificationTestHub(t *testing.T) (*realtime.Hub, *realtime.Client) {
	t.Helper()
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	client := realtime.NewClient(1, 8, "", nil, "WS")
	hub.RegisterClient(client)
	deadline := time.Now().Add(time.Second)
	for hub.ClientCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if hub.ClientCount() != 1 {
		t.Fatal("SSE test client did not register")
	}
	return hub, client
}

type notificationTerminalTabs struct {
	err error
}

func (s *notificationTerminalTabs) ListTabs(context.Context, string) ([]interaction.TabMetadata, error) {
	return nil, s.err
}
func (s *notificationTerminalTabs) GetTab(context.Context, string, string) (*interaction.TabMetadata, error) {
	return nil, s.err
}
func (s *notificationTerminalTabs) PatchTab(context.Context, string, string, map[string]string) (*interaction.PatchTabResult, error) {
	return &interaction.PatchTabResult{Tab: &interaction.TabMetadata{SessionName: "tab-1"}, IssueIDChanged: true}, s.err
}
func (s *notificationTerminalTabs) PutTab(context.Context, interaction.PutTerminalTabCommand) (*interaction.TabMetadata, error) {
	return &interaction.TabMetadata{SessionName: "tab-1"}, s.err
}
func (s *notificationTerminalTabs) DeleteTab(context.Context, string, string) error {
	return s.err
}
func (s *notificationTerminalTabs) ListSessionsByIssue(context.Context) (map[string][]string, error) {
	return nil, s.err
}
func (s *notificationTerminalTabs) EnsureAgentTerminal(context.Context, interaction.EnsureAgentTerminalCommand) (*interaction.TabMetadata, error) {
	return nil, s.err
}
func (s *notificationTerminalTabs) PlanTerminalAttach(context.Context, interaction.TerminalAttachCommand) (interaction.TerminalAttachPlan, error) {
	return interaction.TerminalAttachPlan{}, s.err
}
func (s *notificationTerminalTabs) AttachTerminal(context.Context, interaction.TerminalAttachCommand) (*interaction.TerminalAttachResult, error) {
	return nil, s.err
}
func (s *notificationTerminalTabs) DetachTerminal(context.Context, string, string, string) {}
func (s *notificationTerminalTabs) AgentTerminalInfo(context.Context, string, string) (*interaction.AgentTerminalInfo, error) {
	return nil, s.err
}
func (s *notificationTerminalTabs) AttachAgentTerminal(context.Context, interaction.AttachAgentTerminalCommand) (*interaction.AgentTerminalAttachResult, error) {
	return nil, s.err
}
func (s *notificationTerminalTabs) DetachAgentTerminal(context.Context, string) error { return s.err }
func (s *notificationTerminalTabs) StartSetup(context.Context, string, interaction.TerminalSetupRequest) (*interaction.TerminalSetupResult, error) {
	return &interaction.TerminalSetupResult{SessionName: "tab-1"}, s.err
}

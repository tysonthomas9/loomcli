package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// `loom daemon answer` — the terminal end of the human answer path.
//
// With no selection it SHOWS the agent's pending prompt (or, with no agent,
// every pending prompt in the workspace), because the operator's first
// question is "what is it asking?", not "how do I say yes". With a selection
// it resolves the prompt: a bare argument matches an option by ID or label,
// --text sends free text, --decline says no human will approve this.

var (
	answerText    string
	answerDecline bool
)

var daemonAnswerCmd = &cobra.Command{
	Use:   "answer [agent-name] [option]",
	Short: "Show or answer an agent's pending interactive prompt",
	Long: `Show or answer the interactive prompt an agent is waiting on.

An agent whose role input_policy says "ask" parks on the harness prompt and
waits for a person. This command is that person's terminal:

  loom daemon answer                    list every agent waiting on input
  loom daemon answer critic             show critic's pending prompt + options
  loom daemon answer critic yes         answer with the option matching "yes"
  loom daemon answer critic --text "use the staging bucket"
  loom daemon answer critic --decline   refuse the prompt (the agent denies it)`,
	Args: cobra.RangeArgs(0, 2),
	Run: func(_ *cobra.Command, args []string) {
		socketPath, err := resolveControlSocketFromCwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		agent := ""
		if len(args) > 0 {
			agent = args[0]
		}
		selection := ""
		if len(args) > 1 {
			selection = args[1]
		}
		if selection == "" && answerText == "" && !answerDecline {
			showPendingInputs(socketPath, agent)
			return
		}
		if agent == "" {
			fmt.Fprintln(os.Stderr, "Error: answering requires an agent name")
			os.Exit(1)
		}
		answerPendingInput(socketPath, agent, selection, answerText, answerDecline)
	},
}

func init() {
	daemonAnswerCmd.Flags().StringVar(&answerText, "text", "", "Answer with free text instead of an option")
	daemonAnswerCmd.Flags().BoolVar(&answerDecline, "decline", false, "Refuse the prompt; the agent treats it as denied")
}

// fetchPendingInputs asks the daemon for pending prompts (all when agent is
// empty).
func fetchPendingInputs(socketPath, agent string) ([]PendingInput, error) {
	resp, err := sendDaemonControlRequestFull(socketPath, DaemonControlRequest{
		Operation: ctrlOpInputGet,
		AgentName: agent,
	})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var pending []PendingInput
	if err := json.Unmarshal(resp.Data, &pending); err != nil {
		return nil, fmt.Errorf("malformed pending-input payload: %w", err)
	}
	return pending, nil
}

func showPendingInputs(socketPath, agent string) {
	pending, err := fetchPendingInputs(socketPath, agent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(pending) == 0 {
		if agent == "" {
			fmt.Println("No agent is waiting on input.")
		} else {
			fmt.Printf("Agent %q is not waiting on input.\n", agent)
		}
		return
	}
	for _, p := range pending {
		fmt.Printf("%s is waiting on input (kind %s, asked %s ago):\n",
			p.Agent, p.Kind, time.Since(p.AskedAt).Truncate(time.Second))
		for _, line := range strings.Split(strings.TrimRight(p.Prompt, "\n"), "\n") {
			fmt.Printf("  %s\n", line)
		}
		for _, o := range p.Options {
			if o.Label != "" && o.Label != o.ID {
				fmt.Printf("    [%s] %s\n", o.ID, o.Label)
			} else {
				fmt.Printf("    [%s]\n", o.ID)
			}
		}
		fmt.Printf("  answer with: loom daemon answer %s <option> | --text \"...\" | --decline\n", p.Agent)
	}
}

func answerPendingInput(socketPath, agent, selection, text string, decline bool) {
	optionID := ""
	requestID := ""
	if selection != "" {
		// Resolve the selection against the live prompt so a label works as
		// well as an ID, and carry the request id so the answer cannot land on
		// a prompt that replaced the one the operator read.
		pending, err := fetchPendingInputs(socketPath, agent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(pending) == 0 {
			fmt.Fprintf(os.Stderr, "Error: agent %q is not waiting on input\n", agent)
			os.Exit(1)
		}
		p := pending[0]
		requestID = p.RequestID
		match := matchOption(p.Options, selection)
		if match == "" {
			fmt.Fprintf(os.Stderr, "Error: prompt offers no option matching %q (options: %s)\n",
				selection, optionIDs(p.Options))
			os.Exit(1)
		}
		optionID = match
	}

	args, _ := json.Marshal(controlInputAnswerArgs{
		RequestID: requestID,
		OptionID:  optionID,
		Text:      text,
		Decline:   decline,
	})
	resp, err := sendDaemonControlRequestFull(socketPath, DaemonControlRequest{
		Operation: ctrlOpInputAnswer,
		AgentName: agent,
		Args:      args,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !resp.Success {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}
	switch {
	case decline:
		fmt.Printf("Declined %s's prompt.\n", agent)
	case text != "":
		fmt.Printf("Answered %s with text.\n", agent)
	default:
		fmt.Printf("Answered %s with option %s.\n", agent, optionID)
	}
}

// matchOption resolves a user selection against the prompt's options: exact ID
// first, then case-insensitive ID, then case-insensitive label. Ambiguity is
// impossible by construction — IDs are unique, and a label collision resolves
// to the first offered option, which is also the order the prompt displayed.
func matchOption(opts []PendingInputOption, selection string) string {
	for _, o := range opts {
		if o.ID == selection {
			return o.ID
		}
	}
	lower := strings.ToLower(selection)
	for _, o := range opts {
		if strings.ToLower(o.ID) == lower {
			return o.ID
		}
	}
	for _, o := range opts {
		if strings.ToLower(strings.TrimSpace(o.Label)) == lower {
			return o.ID
		}
	}
	return ""
}

package terminal

import (
	"context"
	"encoding/json"
	"log/slog"

	"nhooyr.io/websocket" //nolint:staticcheck // SA1019: websocket migration tracked separately

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal/proto"
)

const terminalV1Subprotocol = "loom-terminal.v1"

type relayResult struct {
	status websocket.StatusCode //nolint:staticcheck // SA1019
	reason string
}

func runTerminalRelayV1(ctx context.Context, conn *websocket.Conn, p *terminalWSParams, key webuterminal.SessionKey, att webuterminal.Attachment) (websocket.StatusCode, string) { //nolint:staticcheck // SA1019
	initial := att.InitialState()
	frame, err := proto.Encode(proto.Frame{
		Kind: proto.KindInitialState, Generation: initial.Generation, Sequence: initial.Sequence,
		Cols: initial.Cols, Rows: initial.Rows, RetainedLines: initial.RetainedLines, Encoding: initial.Encoding, Data: initial.Data,
	})
	if err != nil {
		return websocket.StatusInternalError, "initial state encode failed" //nolint:staticcheck
	}
	if err := conn.Write(ctx, websocket.MessageBinary, frame); err != nil { //nolint:staticcheck
		p.manager.Detach(key, att.ConnID())
		return websocket.StatusInternalError, "initial state write failed" //nolint:staticcheck
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pumpResult := make(chan relayResult, 1)
	readResult := make(chan relayResult, 1)
	go pumpTerminalV1(ctx, conn, att, pumpResult)
	go readTerminalV1(ctx, conn, att, readResult)

	var result relayResult
	select {
	case result = <-pumpResult:
		_ = conn.Close(result.status, result.reason) //nolint:staticcheck
		cancel()
	case result = <-readResult:
		_ = conn.Close(result.status, result.reason) //nolint:staticcheck
		cancel()
	}
	p.manager.Detach(key, att.ConnID())
	return result.status, result.reason
}

func pumpTerminalV1(ctx context.Context, conn *websocket.Conn, att webuterminal.Attachment, result chan<- relayResult) { //nolint:staticcheck
	initial := att.InitialState()
	lastDeliveredSequence := initial.Sequence
	lastKind := proto.KindInitialState
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-att.Output():
			if !ok {
				if lastKind != proto.KindClose {
					closeFrame, err := proto.Encode(proto.Frame{
						Kind: proto.KindClose, Generation: initial.Generation,
						Sequence: lastDeliveredSequence + 1, Reason: string(att.CloseReason()),
					})
					if err == nil {
						_ = conn.Write(ctx, websocket.MessageBinary, closeFrame) //nolint:staticcheck
					}
				}
				status, reason := terminalCloseStatus(att.CloseReason())
				result <- relayResult{status: status, reason: reason}
				return
			}
			frame, err := eventFrame(att.InitialState().Generation, event)
			if err != nil {
				result <- relayResult{status: websocket.StatusInternalError, reason: "event encode failed"} //nolint:staticcheck
				return
			}
			if err := conn.Write(ctx, websocket.MessageBinary, frame); err != nil { //nolint:staticcheck
				result <- relayResult{status: websocket.StatusInternalError, reason: "terminal write failed"} //nolint:staticcheck
				return
			}
			lastDeliveredSequence = event.Sequence
			lastKind = frame[3]
		}
	}
}

func eventFrame(generation webuterminal.Generation, event webuterminal.TerminalEvent) ([]byte, error) {
	f := proto.Frame{Generation: generation, Sequence: event.Sequence}
	switch event.Kind {
	case webuterminal.EventOutput:
		f.Kind, f.Data = proto.KindOutput, event.Data
	case webuterminal.EventResize:
		f.Kind, f.Cols, f.Rows = proto.KindResize, event.Cols, event.Rows
	case webuterminal.EventNotice:
		f.Kind = proto.KindNotice
		var notice struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			ConnID  string `json:"conn_id"`
		}
		if err := json.Unmarshal(event.Data, &notice); err != nil {
			return nil, err
		}
		f.Code, f.Message, f.ConnID = notice.Code, notice.Message, notice.ConnID
	case webuterminal.EventClose:
		f.Kind, f.Reason = proto.KindClose, string(event.Data)
	default:
		return nil, proto.ErrUnknownKind
	}
	return proto.Encode(f)
}

func readTerminalV1(ctx context.Context, conn *websocket.Conn, att webuterminal.Attachment, result chan<- relayResult) { //nolint:staticcheck
	for {
		_, data, err := conn.Read(ctx) //nolint:staticcheck
		if err != nil {
			result <- relayResult{status: websocket.StatusNormalClosure, reason: "client closed"} //nolint:staticcheck
			return
		}
		frame, err := proto.Decode(data)
		if err != nil {
			result <- relayResult{status: websocket.StatusProtocolError, reason: "malformed terminal frame"} //nolint:staticcheck
			return
		}
		generation := att.InitialState().Generation
		if frame.Generation != generation {
			slog.Debug("ignoring terminal frame from mismatched generation")
			continue
		}
		switch frame.Kind {
		case proto.KindInput:
			if _, err := att.WriteInput(frame.Data); err != nil {
				slog.Debug("terminal input rejected", "err", err)
			}
		case proto.KindResizeRequest:
			if frame.Cols == 0 || frame.Rows == 0 || frame.Cols > realtime.MaxTerminalCols || frame.Rows > realtime.MaxTerminalRows {
				continue
			}
			if err := att.RequestResize(frame.Cols, frame.Rows); err != nil {
				slog.Debug("terminal resize rejected", "err", err)
			}
		case proto.KindFocus:
			if err := att.Focus(); err != nil {
				slog.Debug("terminal focus rejected", "err", err)
			}
		default:
			result <- relayResult{status: websocket.StatusProtocolError, reason: "unexpected client frame"} //nolint:staticcheck
			return
		}
	}
}

func terminalCloseStatus(reason webuterminal.CloseReason) (websocket.StatusCode, string) { //nolint:staticcheck
	switch reason {
	case webuterminal.CloseExited:
		return websocket.StatusCode(4001), "exited" //nolint:staticcheck
	case webuterminal.CloseKilled:
		return websocket.StatusCode(4002), "killed" //nolint:staticcheck
	case webuterminal.CloseShutdown:
		return websocket.StatusGoingAway, "shutdown" //nolint:staticcheck
	case webuterminal.CloseSlowConsumer:
		return websocket.StatusCode(4003), "slow consumer; resnapshot required" //nolint:staticcheck
	case webuterminal.CloseStateRebuild:
		return websocket.StatusCode(4004), "state rebuilding; retry" //nolint:staticcheck
	default:
		return websocket.StatusNormalClosure, "replaced" //nolint:staticcheck
	}
}

package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// EpicStatusResponse wraps the epic status data for JSON response.
type EpicStatusResponse struct {
	Success bool                `json:"success"`
	Data    []*types.EpicStatus `json:"data,omitempty"`
	Error   string              `json:"error,omitempty"`
}

// epicStatusClient is an internal interface for testing epic status operations.
type epicStatusClient interface {
	EpicStatus(args *rpc.EpicStatusArgs) (*rpc.Response, error)
}

// epicStatusConnectionGetter is an internal interface for testing epic status handler pool operations.
type epicStatusConnectionGetter interface {
	Get(ctx context.Context) (epicStatusClient, error)
	Put(client epicStatusClient)
}

// epicStatusPoolAdapter wraps daemon.Pool to implement epicStatusConnectionGetter.
type epicStatusPoolAdapter struct {
	pool daemon.Pool
}

func (p *epicStatusPoolAdapter) Get(ctx context.Context) (epicStatusClient, error) {
	return p.pool.Get(ctx)
}

func (p *epicStatusPoolAdapter) Put(client epicStatusClient) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// handleGetEpicStatus returns a handler that retrieves epic completion statuses.
func handleGetEpicStatus(pool daemon.Pool) http.HandlerFunc {
	if pool == nil {
		return handleGetEpicStatusWithPool(nil)
	}
	return handleGetEpicStatusWithPool(&epicStatusPoolAdapter{pool: pool})
}

// handleGetEpicStatusWithPool is the internal implementation that accepts an interface for testing.
func handleGetEpicStatusWithPool(pool epicStatusConnectionGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			respondJSON(w, http.StatusServiceUnavailable, EpicStatusResponse{
				Success: false,
				Error:   "connection pool not initialized",
			})
			return
		}

		// Parse optional query parameter
		eligibleOnly := r.URL.Query().Get("eligible_only") == "true"

		// Acquire connection with timeout
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		client, err := pool.Get(ctx)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			respondJSON(w, status, EpicStatusResponse{Success: false, Error: err.Error()})
			return
		}
		defer pool.Put(client)

		// Execute EpicStatus RPC call
		resp, err := client.EpicStatus(&rpc.EpicStatusArgs{
			EligibleOnly: eligibleOnly,
		})
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, EpicStatusResponse{
				Success: false,
				Error:   fmt.Sprintf("rpc error: %v", err),
			})
			return
		}

		if !resp.Success {
			respondJSON(w, http.StatusInternalServerError, EpicStatusResponse{
				Success: false,
				Error:   resp.Error,
			})
			return
		}

		// Parse the epic statuses from RPC response
		if len(resp.Data) == 0 {
			respondJSON(w, http.StatusOK, EpicStatusResponse{Success: true, Data: []*types.EpicStatus{}})
			return
		}

		var statuses []*types.EpicStatus
		if err := json.Unmarshal(resp.Data, &statuses); err != nil {
			respondJSON(w, http.StatusInternalServerError, EpicStatusResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to parse epic statuses: %v", err),
			})
			return
		}

		respondJSON(w, http.StatusOK, EpicStatusResponse{Success: true, Data: statuses})
	}
}

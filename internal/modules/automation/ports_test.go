package automation

import (
	"errors"
	"testing"
)

func TestValidateExecutionDispatchRequestDeliveryCAS(t *testing.T) {
	tests := []struct {
		name    string
		request ExecutionDispatchRequest
		wantErr bool
	}{
		{name: "manual"},
		{name: "manual replay", request: ExecutionDispatchRequest{
			WorkspaceKey: "ws", TriggerBindingID: "binding-1", IdempotencyKey: "manual-key", ReplayOnly: true,
		}},
		{name: "manual replay missing identity", request: ExecutionDispatchRequest{ReplayOnly: true}, wantErr: true},
		{name: "reserved", request: ExecutionDispatchRequest{
			DeliveryID: "delivery-1", ExpectedDeliveryStatus: DeliveryAccepted, ExpectedDeliveryAttempt: 1,
		}},
		{name: "noncanonical delivery", request: ExecutionDispatchRequest{
			DeliveryID: " delivery-1 ", ExpectedDeliveryStatus: DeliveryAccepted, ExpectedDeliveryAttempt: 1,
		}, wantErr: true},
		{name: "missing status", request: ExecutionDispatchRequest{
			DeliveryID: "delivery-1", ExpectedDeliveryAttempt: 1,
		}, wantErr: true},
		{name: "invalid status", request: ExecutionDispatchRequest{
			DeliveryID: "delivery-1", ExpectedDeliveryStatus: DeliveryStatus("unknown"), ExpectedDeliveryAttempt: 1,
		}, wantErr: true},
		{name: "missing attempt", request: ExecutionDispatchRequest{
			DeliveryID: "delivery-1", ExpectedDeliveryStatus: DeliveryAccepted,
		}, wantErr: true},
		{name: "manual with cas", request: ExecutionDispatchRequest{
			ExpectedDeliveryStatus: DeliveryAccepted, ExpectedDeliveryAttempt: 1,
		}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateExecutionDispatchRequest(test.request)
			if test.wantErr && !errors.Is(err, ErrInvalidPersistedState) {
				t.Fatalf("error = %v, want ErrInvalidPersistedState", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

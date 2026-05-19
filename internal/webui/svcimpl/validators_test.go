package svcimpl

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestClassifyStoreError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind service.ErrorKind
	}{
		{"nil", nil, ""},
		{"not found", domain.ErrNotFound, service.KindNotFound},
		{"already exists", domain.ErrAlreadyExists, service.KindConflict},
		{"invalid", domain.ErrInvalid, service.KindValidation},
		{"conflict", domain.ErrConflict, service.KindConflict},
		{"internal", errors.New("boom"), service.KindInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyStoreError("op", tt.err)
			if tt.err == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			var svcErr *service.ServiceError
			if !errors.As(err, &svcErr) {
				t.Fatalf("err %T is not ServiceError: %v", err, err)
			}
			if svcErr.Kind != tt.kind {
				t.Fatalf("kind = %s, want %s", svcErr.Kind, tt.kind)
			}
		})
	}
}

package cmdstore

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

func TestConnectorManagementComposesOwnerAPI(t *testing.T) {
	backend := memstore.New()
	management, err := ConnectorManagement(&bootstrap.StoreHandle{Store: backend})
	if err != nil {
		t.Fatalf("compose management: %v", err)
	}
	created, err := management.CreateConnector(context.Background(), connectorsmodule.CreateConnectorCommand{
		WorkspaceKey: "WS", ConnectorID: "github", SourceKind: connectorsmodule.ConnectorSourceGitHub,
	})
	if err != nil || created.ConnectorID != "github" {
		t.Fatalf("create connector = %+v, %v", created, err)
	}
}

func TestConnectorManagementFailsClosedWithoutStore(t *testing.T) {
	for _, handle := range []*bootstrap.StoreHandle{nil, {}} {
		if _, err := ConnectorManagement(handle); !errors.Is(err, connectorsmodule.ErrUnavailable) {
			t.Fatalf("ConnectorManagement(%+v) error = %v", handle, err)
		}
	}
}

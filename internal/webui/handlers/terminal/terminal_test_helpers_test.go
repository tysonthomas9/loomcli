package terminal

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/infra/localredis"
)

func newTabMetaStoreForWSTest(t *testing.T) *localredis.TabMetadataStore {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return localredis.NewTabMetadataStore(client, nil)
}

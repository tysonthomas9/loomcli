package outbox

import "github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"

func Open(path string) (JSONLStore, error) {
	return journal.OpenSQLite(path)
}

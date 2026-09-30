package outbox

import "github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"

func Open(path string) (Store, error) {
	return journal.OpenSQLite(path)
}

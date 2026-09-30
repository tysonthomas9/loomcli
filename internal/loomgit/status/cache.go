package status

import (
	"context"

	"golang.org/x/sync/singleflight"
)

var scans singleflight.Group

func Read(ctx context.Context) (Snapshot, error) {
	value, err, _ := scans.Do("status", func() (any, error) { return Scan(ctx, false) })
	if err != nil {
		return Snapshot{}, err
	}
	return value.(Snapshot), nil
}

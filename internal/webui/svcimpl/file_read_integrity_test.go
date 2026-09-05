package svcimpl

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/skillpaths"
)

type failingDirectoryEntry struct {
	name string
	mode fs.FileMode
	err  error
}

func (e failingDirectoryEntry) Name() string               { return e.name }
func (e failingDirectoryEntry) IsDir() bool                { return e.mode.IsDir() }
func (e failingDirectoryEntry) Type() fs.FileMode          { return e.mode }
func (e failingDirectoryEntry) Info() (fs.FileInfo, error) { return nil, e.err }

func TestDirectoryMetadataFailureCannotAcknowledgePartialList(t *testing.T) {
	failure := errors.New("metadata read failed")
	entries, err := convertDirEntries(t.Context(), "", []os.DirEntry{failingDirectoryEntry{name: "visible", err: failure}}, nil, skillpaths.Policy{})
	require.ErrorIs(t, err, failure)
	require.Nil(t, entries)
}

func TestDirectoryIntentionalOmissionsRemainEmptyList(t *testing.T) {
	entries, err := convertDirEntries(t.Context(), "", []os.DirEntry{
		failingDirectoryEntry{name: ".git", err: os.ErrPermission},
		failingDirectoryEntry{name: "link", mode: os.ModeSymlink, err: os.ErrPermission},
		failingDirectoryEntry{name: "vanished", err: os.ErrNotExist},
	}, map[string]bool{".git": true}, skillpaths.Policy{})
	require.NoError(t, err)
	require.NotNil(t, entries)
	require.Empty(t, entries)
}

func TestFilePreviewAlwaysSerializesExplicitContent(t *testing.T) {
	for _, result := range []service.FileReadResult{
		{Path: "empty", Content: "", Version: "sha256:empty"},
		{Path: "large", Content: "prefix", Size: 100, Truncated: true, Version: "sha256:full"},
		{Path: "binary", Size: 100, Binary: true, Truncated: true, Version: "sha256:binary"},
	} {
		t.Run(result.Path, func(t *testing.T) {
			data, err := json.Marshal(result)
			require.NoError(t, err)
			var decoded map[string]any
			require.NoError(t, json.Unmarshal(data, &decoded))
			require.Equal(t, result.Content, decoded["content"])
			require.Equal(t, result.Binary, decoded["binary"])
			require.Equal(t, result.Truncated, decoded["truncated"])
		})
	}
}

package workflows

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/driver/nativearchive"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/infra/workflowdistribution/authoring"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type registerNativeDriverRequest struct {
	Archive      []byte                           `json:"archive"`
	Manifest     []byte                           `json:"manifest,omitempty"`
	DriverName   string                           `json:"driver_name,omitempty"`
	DriverID     string                           `json:"driver_id,omitempty"`
	WorkflowName string                           `json:"workflow_name,omitempty"`
	SourceRef    string                           `json:"source_ref,omitempty"`
	SourceDigest string                           `json:"source_digest,omitempty"`
	Activate     bool                             `json:"activate,omitempty"`
	Trust        workflowcatalog.DriverTrustLevel `json:"trust"`
}

//nolint:cyclop,funlen // Keep staged archive validation, scoped authority resolution, and catalog authoring in one compensating transaction.
func (m *Module) registerNativeDriver(w http.ResponseWriter, r *http.Request) {
	workspace := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspace == "" {
		writeError(w, http.StatusBadRequest, "canonical workspace is required")
		return
	}
	if m == nil || m.catalog == nil || m.authoring == nil || m.catalogAuthority == nil {
		writeDomainError(w, workflowcatalog.ErrUnavailable, "Workflow Catalog native authoring is unavailable")
		return
	}

	var request registerNativeDriverRequest
	err := handler.DecodeOneJSON(w, r, &request, handler.JSONDecodeOptions{
		MaxBytes: nativearchive.MaxRequestBytes, DisallowUnknownFields: true,
	})
	if errors.Is(err, handler.ErrTrailingJSON) {
		writeError(w, http.StatusBadRequest, "native driver registration must contain one JSON value")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid native driver registration JSON")
		return
	}
	if err := nativearchive.ValidateArchiveSize(len(request.Archive)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := nativearchive.ValidateManifestSize(len(request.Manifest)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch request.Trust {
	case workflowcatalog.DriverTrustTrusted, workflowcatalog.DriverTrustUntrusted:
	default:
		writeError(w, http.StatusBadRequest, "native driver trust must be trusted or untrusted")
		return
	}
	if request.Activate && request.Trust == workflowcatalog.DriverTrustUntrusted {
		writeError(w, http.StatusBadRequest, "an explicitly untrusted native driver cannot be activated because activation requires prior approval")
		return
	}

	authorities, err := resolveNativeDriverAuthorities(
		r,
		m.catalogAuthority,
		workspace,
		request.Trust,
		request.Activate,
	)
	if err != nil {
		writeDomainError(w, err, "resolve native Driver authoring authority failed")
		return
	}

	stagingRoot, err := os.MkdirTemp("", "loom-native-driver-upload-*")
	if err != nil {
		writeDomainError(w, err, "create native driver upload staging failed")
		return
	}
	defer os.RemoveAll(stagingRoot) //nolint:errcheck
	distPath := filepath.Join(stagingRoot, "dist")
	if err := extractNativeDriverArchive(request.Archive, distPath); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	manifestPath := ""
	if len(request.Manifest) > 0 {
		manifestPath = filepath.Join(stagingRoot, "loom-driver.json")
		if err := os.WriteFile(manifestPath, request.Manifest, 0o600); err != nil {
			writeDomainError(w, err, "stage native driver manifest failed")
			return
		}
	}
	workDir := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR"))
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			writeDomainError(w, err, "resolve native driver runtime directory failed")
			return
		}
	}

	coordinator, err := appworkflowauthoring.NewWithNative(
		workflowdefs.NewBundleStager(),
		workflowdefs.NewNativeBundleStager(),
	)
	if err != nil {
		writeDomainError(w, err, "initialize native driver authoring failed")
		return
	}
	result, err := coordinator.AuthorNative(
		r.Context(),
		m.catalog,
		m.authoring,
		authorities,
		appworkflowauthoring.NativeOptions{
			WorkspaceKey: workspace,
			WorkDir:      workDir,
			DistPath:     distPath,
			ManifestPath: manifestPath,
			DriverName:   request.DriverName,
			DriverID:     request.DriverID,
			WorkflowName: request.WorkflowName,
			SourceRef:    request.SourceRef,
			SourceDigest: request.SourceDigest,
			Activate:     request.Activate,
			Trust:        request.Trust,
		},
	)
	if err != nil {
		writeDomainError(w, err, "author native driver failed")
		return
	}
	// Bundle.Root is a server-local filesystem coordinate and is not part of
	// the management response. Durable BundleRef/BundleDigest remain on Version.
	result.Bundle = nil
	handler.WriteJSON(w, http.StatusCreated, result)
}

func resolveNativeDriverAuthorities(
	r *http.Request,
	resolver workflowcataloghttp.OperatorAuthorityResolver,
	workspace string,
	trust workflowcatalog.DriverTrustLevel,
	activate bool,
) (appworkflowauthoring.NativeAuthoringAuthorities, error) {
	if r == nil || resolver == nil {
		return appworkflowauthoring.NativeAuthoringAuthorities{}, workflowcatalog.ErrUnavailable
	}
	resolve := func(action authority.Action) (authority.OperatorAuthority, error) {
		return resolver.ResolveOperatorAuthority(r, workspace, action)
	}
	author, err := resolve(workflowcatalog.ActionAuthorVersion)
	if err != nil {
		return appworkflowauthoring.NativeAuthoringAuthorities{}, err
	}
	result := appworkflowauthoring.NativeAuthoringAuthorities{Author: author}
	if trust.Trusted() || activate {
		approve, approveErr := resolve(workflowcatalog.ActionApproveVersion)
		if approveErr != nil {
			return appworkflowauthoring.NativeAuthoringAuthorities{}, approveErr
		}
		result.Approve = &approve
	}
	if activate {
		activation, activateErr := resolve(workflowcatalog.ActionActivateVersion)
		if activateErr != nil {
			return appworkflowauthoring.NativeAuthoringAuthorities{}, activateErr
		}
		result.Activate = &activation
	}
	return result, nil
}

//nolint:funlen,gocognit // Keep path containment, archive accounting, extraction, and close checks in one security boundary.
func extractNativeDriverArchive(data []byte, destination string) error {
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("decode native driver archive: %w", err)
	}
	defer compressed.Close() //nolint:errcheck
	archive := tar.NewReader(compressed)
	seen := map[string]struct{}{}
	entryCount := 0
	var extractedBytes int64
	for {
		header, nextErr := archive.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read native driver archive: %w", nextErr)
		}
		clean, err := nativearchive.CleanEntryName(header.Name)
		if err != nil {
			return err
		}
		if _, duplicate := seen[clean]; duplicate {
			return fmt.Errorf("native driver archive contains duplicate path %q", clean)
		}
		seen[clean] = struct{}{}
		target := filepath.Join(destination, filepath.FromSlash(clean))
		relative, relErr := filepath.Rel(destination, target)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("native driver archive path %q escapes its staging root", clean)
		}
		kind, err := nativearchive.ClassifyEntry(header.Typeflag, clean)
		if err != nil {
			return err
		}
		var accountErr error
		entryCount, extractedBytes, accountErr = nativearchive.AccountEntry(
			entryCount,
			extractedBytes,
			header.Size,
			kind == nativearchive.EntryRegularFile,
		)
		if accountErr != nil {
			return accountErr
		}
		switch kind {
		case nativearchive.EntryDirectory:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create native driver archive directory: %w", err)
			}
		case nativearchive.EntryRegularFile:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create native driver archive parent: %w", err)
			}
			// target is derived from CleanEntryName and proven beneath the private staging root above.
			//nolint:gosec // G304: the validated, contained archive path is the intended dynamic output.
			file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return fmt.Errorf("create native driver archive file: %w", err)
			}
			_, copyErr := io.CopyN(file, archive, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return fmt.Errorf("extract native driver archive file: %w", copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close native driver archive file: %w", closeErr)
			}
		}
	}
	return nil
}

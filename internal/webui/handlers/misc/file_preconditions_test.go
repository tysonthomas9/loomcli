package misc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

type preconditionRecordingFileService struct {
	stubFileService
	readResult      *sourcecontrol.FileReadResult
	writeResult     *sourcecontrol.FileMutationResult
	writeErr        error
	writeConditions sourcecontrol.FileWritePreconditions
	deleteErr       error
	deleteVersion   string
	moveResult      *sourcecontrol.FileMutationResult
	moveErr         error
	moveSource      string
	moveDestination string
}

func (s *preconditionRecordingFileService) ReadFile(_ context.Context, _ sourcecontrol.PathQuery) (*sourcecontrol.FileReadResult, error) {
	return s.readResult, nil
}

func (s *preconditionRecordingFileService) WriteFile(_ context.Context, command sourcecontrol.WriteCommand) (*sourcecontrol.FileMutationResult, error) {
	s.writeConditions = sourcecontrol.FileWritePreconditions{IfMatch: command.ExpectedVersion, IfNoneMatch: command.CreateOnly}
	return s.writeResult, s.writeErr
}

func (s *preconditionRecordingFileService) DeletePath(_ context.Context, command sourcecontrol.DeleteCommand) error {
	s.deleteVersion = command.ExpectedVersion
	return s.deleteErr
}

func (s *preconditionRecordingFileService) MovePath(_ context.Context, command sourcecontrol.MoveCommand) (*sourcecontrol.FileMutationResult, error) {
	s.moveSource = command.ExpectedSourceVersion
	s.moveDestination = command.ExpectedDestinationVersion
	return s.moveResult, s.moveErr
}

func TestHandleScopedFileRead_EmitsStrongETag(t *testing.T) {
	svc := &preconditionRecordingFileService{readResult: &sourcecontrol.FileReadResult{Path: "file.txt", Version: "sha256:abc"}}
	w := httptest.NewRecorder()
	HandleScopedFileRead(svc).ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files?path=file.txt"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("ETag"); got != `"sha256:abc"` {
		t.Fatalf("ETag = %q", got)
	}
}

func TestHandleScopedFileWrite_ForwardsConditionalHeaders(t *testing.T) {
	svc := &preconditionRecordingFileService{writeResult: &sourcecontrol.FileMutationResult{Success: true, Version: "sha256:new"}}
	req := scopedReqBody(http.MethodPut, "/api/workspaces/test-ws/files?path=file.txt", `{"content":"new"}`)
	req.Header.Set("If-Match", `"sha256:old"`)
	w := httptest.NewRecorder()
	HandleScopedFileWrite(svc).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if svc.writeConditions.IfMatch != "sha256:old" || svc.writeConditions.IfNoneMatch {
		t.Fatalf("conditions = %+v", svc.writeConditions)
	}
	if got := w.Header().Get("ETag"); got != `"sha256:new"` {
		t.Fatalf("ETag = %q", got)
	}
}

func TestHandleScopedFileWrite_CreateOnlyAndStaleMappings(t *testing.T) {
	svc := &preconditionRecordingFileService{writeErr: sourcecontrol.ErrPreconditionFailed}
	req := scopedReqBody(http.MethodPut, "/api/workspaces/test-ws/files?path=file.txt", `{"content":"new"}`)
	req.Header.Set("If-None-Match", "*")
	w := httptest.NewRecorder()
	HandleScopedFileWrite(svc).ServeHTTP(w, req)
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412; body=%s", w.Code, w.Body.String())
	}
	if !svc.writeConditions.IfNoneMatch {
		t.Fatalf("conditions = %+v", svc.writeConditions)
	}
}

func TestHandleScopedFileDelete_PreconditionRequiredAndForwarded(t *testing.T) {
	svc := &preconditionRecordingFileService{deleteErr: sourcecontrol.ErrPreconditionRequired}
	w := httptest.NewRecorder()
	HandleScopedFileDelete(svc).ServeHTTP(w, scopedReq("/api/workspaces/test-ws/files?path=file.txt"))
	if w.Code != http.StatusPreconditionRequired {
		t.Fatalf("status = %d, want 428; body=%s", w.Code, w.Body.String())
	}

	svc.deleteErr = nil
	req := scopedReq("/api/workspaces/test-ws/files?path=file.txt")
	req.Header.Set("If-Match", `"sha256:current"`)
	w = httptest.NewRecorder()
	HandleScopedFileDelete(svc).ServeHTTP(w, req)
	if w.Code != http.StatusOK || svc.deleteVersion != "sha256:current" {
		t.Fatalf("status=%d version=%q body=%s", w.Code, svc.deleteVersion, w.Body.String())
	}
}

func TestHandleScopedFileMove_ForwardsBothVersions(t *testing.T) {
	svc := &preconditionRecordingFileService{moveResult: &sourcecontrol.FileMutationResult{Success: true, Version: "sha256:source"}}
	req := scopedReqBody(http.MethodPatch, "/api/workspaces/test-ws/files/move", `{"from":"a","to":"b","overwrite":true,"source_version":"sha256:source","destination_version":"sha256:destination"}`)
	w := httptest.NewRecorder()
	HandleScopedFileMove(svc).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if svc.moveSource != "sha256:source" || svc.moveDestination != "sha256:destination" {
		t.Fatalf("versions = %q, %q", svc.moveSource, svc.moveDestination)
	}
}

func TestParseStrongETagRejectsWeakOrMultipleValues(t *testing.T) {
	for _, value := range []string{`W/"sha256:x"`, `"sha256:x", "sha256:y"`, "sha256:unquoted"} {
		if _, err := parseStrongETag(value); err == nil {
			t.Fatalf("parseStrongETag(%q) succeeded", value)
		}
	}
}

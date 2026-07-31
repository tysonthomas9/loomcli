package sourcecontrol

import (
	"errors"
	"strings"
	"testing"
)

func TestRepositoryAdmissionMaterializationIDBindsExactOwnerGeneration(t *testing.T) {
	t.Parallel()

	command := RepositoryAdmissionCheckoutCommand{
		WorkspaceKey:      "WORK",
		AdmissionID:       "0123456789abcdef0123456789abcdef",
		RepositoryRef:     "app",
		OwnerID:           "loom-workspace-admission-owner",
		OwnerGenerationID: "abcdef0123456789abcdef0123456789",
		SpecFingerprint:   "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	materializationID, err := RepositoryAdmissionMaterializationID(command)
	if err != nil {
		t.Fatal(err)
	}
	admissionID, matched, err :=
		ParseRepositoryAdmissionMaterializationID(materializationID)
	if err != nil ||
		!matched ||
		admissionID != command.AdmissionID ||
		strings.Contains(materializationID, command.OwnerID) ||
		strings.Contains(materializationID, command.OwnerGenerationID) {
		t.Fatalf(
			"materialization identity = %q, %q, %t, %v",
			materializationID,
			admissionID,
			matched,
			err,
		)
	}

	successor := command
	successor.OwnerGenerationID = "11111111111111111111111111111111"
	successorID, err := RepositoryAdmissionMaterializationID(successor)
	if err != nil {
		t.Fatal(err)
	}
	if successorID == materializationID {
		t.Fatal("successor owner generation reused prior materialization identity")
	}
}

func TestParseRepositoryAdmissionMaterializationIDFailsClosed(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"repository-admission:",
		"repository-admission:0123456789abcdef0123456789abcdef",
		"repository-admission:0123456789abcdef0123456789abcdef:not-a-digest",
		"repository-admission:0123456789abcdef0123456789abcdef:" +
			strings.Repeat("a", 64) + ":suffix",
	} {
		if _, matched, err := ParseRepositoryAdmissionMaterializationID(value); !matched ||
			!errors.Is(err, ErrInvalid) {
			t.Fatalf("ParseRepositoryAdmissionMaterializationID(%q) = matched %t, err %v", value, matched, err)
		}
	}
	if _, matched, err := ParseRepositoryAdmissionMaterializationID("task-run:one"); matched || err != nil {
		t.Fatalf("non-admission identity = matched %t, err %v", matched, err)
	}
}

func TestFetchRejectsRepositoryAdmissionMaterializationIdentity(t *testing.T) {
	t.Parallel()

	materializationID, err := RepositoryAdmissionMaterializationID(
		RepositoryAdmissionCheckoutCommand{
			WorkspaceKey:      "WORK",
			AdmissionID:       "0123456789abcdef0123456789abcdef",
			RepositoryRef:     "app",
			OwnerID:           "loom-workspace-admission-owner",
			OwnerGenerationID: "abcdef0123456789abcdef0123456789",
			SpecFingerprint:   "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = normalizeFetchRefCommand(FetchRefCommand{
		WorkspaceKey:   "WORK",
		OperationID:    materializationID,
		RepositoryRef:  "app",
		SourceRef:      "refs/heads/main",
		DestinationRef: "refs/loom/fetch/main",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("fetch admission identity error = %v, want ErrInvalid", err)
	}
}

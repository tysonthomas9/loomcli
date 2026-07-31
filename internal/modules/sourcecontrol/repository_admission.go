package sourcecontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const repositoryAdmissionMaterializationPrefix = "repository-admission:"

// RepositoryAdmissionMaterializationID converts an exact admission owner
// generation into the opaque operation coordinate used by Source Control.
// Only a digest of the owner fence crosses into the generic materialization
// workflow, so neither the owner ID nor its generation is exposed to Git.
func RepositoryAdmissionMaterializationID(
	command RepositoryAdmissionCheckoutCommand,
) (string, error) {
	if err := ValidateRepositoryAdmissionCheckoutCommand(command); err != nil {
		return "", err
	}
	fence := sha256.Sum256([]byte(
		command.WorkspaceKey + "\x00" +
			command.AdmissionID + "\x00" +
			command.OwnerID + "\x00" +
			command.OwnerGenerationID + "\x00" +
			command.SpecFingerprint,
	))
	return repositoryAdmissionMaterializationPrefix +
		command.AdmissionID + ":" +
		hex.EncodeToString(fence[:]), nil
}

// ParseRepositoryAdmissionMaterializationID reports whether an operation ID
// addresses the admission workflow and, when it does, returns its opaque
// admission ID. Malformed admission-prefixed values fail closed.
func ParseRepositoryAdmissionMaterializationID(
	value string,
) (admissionID string, matched bool, err error) {
	if !strings.HasPrefix(value, repositoryAdmissionMaterializationPrefix) {
		return "", false, nil
	}
	encoded := strings.TrimPrefix(value, repositoryAdmissionMaterializationPrefix)
	admissionID, fence, found := strings.Cut(encoded, ":")
	if !found ||
		strings.Contains(fence, ":") ||
		!validOpaqueAdmissionID(admissionID) ||
		!validSHA256Hex(fence) {
		return "", true, ErrInvalid
	}
	return admissionID, true, nil
}

func validSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

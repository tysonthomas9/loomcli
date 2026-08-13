package sourcecontrol

import (
	"path/filepath"
	"strings"
)

// FileCapabilities are the effective permissions for one workspace file request.
type FileCapabilities struct {
	Read      bool
	Write     bool
	Sensitive bool
}

// AccessGrant is an opaque authorization minted by the authenticated delivery
// boundary and passed explicitly to Source Control commands. Its zero value is
// invalid and fails closed.
type AccessGrant struct {
	read      bool
	write     bool
	sensitive bool
	seal      *accessGrantSeal
}

type accessGrantSeal struct{ marker byte }

// AccessGrantIssuer is an opaque capability created by the composition root
// for one Source Control instance. Grants minted by another issuer are not
// accepted by that instance.
type AccessGrantIssuer struct {
	seal *accessGrantSeal
}

func NewAccessGrantIssuer() AccessGrantIssuer {
	return AccessGrantIssuer{seal: &accessGrantSeal{marker: 1}}
}

func (issuer AccessGrantIssuer) Available() bool {
	return issuer.seal != nil
}

func (issuer AccessGrantIssuer) Read(sensitive bool) AccessGrant {
	return AccessGrant{read: true, sensitive: sensitive, seal: issuer.seal}
}

func (issuer AccessGrantIssuer) ReadWrite(sensitive bool) AccessGrant {
	return AccessGrant{read: true, write: true, sensitive: sensitive, seal: issuer.seal}
}

func (grant AccessGrant) Capabilities() FileCapabilities {
	if grant.seal == nil {
		return FileCapabilities{}
	}
	return FileCapabilities{Read: grant.read, Write: grant.write, Sensitive: grant.sensitive}
}

func requireReadGrant(seal *accessGrantSeal, grant AccessGrant) error {
	if seal == nil || grant.seal != seal || !grant.read {
		return newForbidden("read access grant required")
	}
	return nil
}

func requireWriteGrant(seal *accessGrantSeal, grant AccessGrant) error {
	if seal == nil || grant.seal != seal || !grant.write {
		return newForbidden("write access grant required")
	}
	return nil
}

// IsSensitiveFilePath classifies credential-like files independently of scope.
// Matching is case-insensitive so policy does not vary by filesystem behavior.
func IsSensitiveFilePath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	for _, segment := range strings.Split(clean, "/") {
		if isSensitiveFileName(segment) {
			return true
		}
	}
	return false
}

func isSensitiveFileName(name string) bool {
	base := strings.ToLower(name)
	if base == ".netrc" || strings.HasPrefix(base, ".env") {
		return true
	}
	if isSSHPrivateKeyName(base) {
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".key", ".pem", ".p12", ".pfx", ".crt", ".cer", ".der", ".jks":
		return true
	default:
		return false
	}
}

func isSSHPrivateKeyName(base string) bool {
	switch base {
	case "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "identity":
		return true
	default:
		return strings.HasPrefix(base, "id_rsa_") ||
			strings.HasPrefix(base, "id_dsa_") ||
			strings.HasPrefix(base, "id_ecdsa_") ||
			strings.HasPrefix(base, "id_ed25519_")
	}
}

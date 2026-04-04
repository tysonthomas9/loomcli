package webui

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
)

// generateNotifyToken creates a cryptographically random 32-byte hex-encoded
// token and optionally writes it to <dir>/notify.token (0600). Returns the
// token string and the file path (empty if not written).
func generateNotifyToken(dir string) (token, filePath string) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		logger.Warn("failed to generate notify token, session notifications will be rejected", "err", err)
		return "", ""
	}
	token = hex.EncodeToString(tokenBytes)

	if dir == "" {
		return token, ""
	}
	filePath = filepath.Join(dir, "notify.token")
	if err := os.WriteFile(filePath, []byte(token), 0600); err != nil {
		logger.Warn("failed to write notify token file", "path", filePath, "err", err)
		return token, ""
	}
	logger.Info("notify token written", "path", filePath)
	return token, filePath
}

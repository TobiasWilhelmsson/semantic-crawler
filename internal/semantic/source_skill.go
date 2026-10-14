package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

type SourceSkill struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Hash    string `json:"hash"`
	Version int    `json:"version"`
}

func LoadSourceSkill(path string, version int) (SourceSkill, error) {
	if version <= 0 {
		version = 1
	}
	if strings.TrimSpace(path) == "" {
		return SourceSkill{Version: version}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return SourceSkill{}, fmt.Errorf("read source skill %s: %w", path, err)
	}
	content := strings.TrimSpace(string(data))
	hash := sha256.Sum256([]byte(content))
	return SourceSkill{Path: path, Content: content, Hash: hex.EncodeToString(hash[:]), Version: version}, nil
}

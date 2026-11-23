package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func EmailFile(token string) string {
	return strings.TrimSuffix(token, filepath.Ext(token)) + ".email"
}

func SaveEmail(token, email string) error {
	if err := os.WriteFile(EmailFile(token), []byte(email+"\n"), 0o600); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	return nil
}

func ReadEmail(token string) (string, error) {
	data, err := os.ReadFile(EmailFile(token))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

//go:build !personal

package config

import (
	"encoding/json"
	"os"
)

func LoadAuth() (*Auth, error) {
	data, err := os.ReadFile(AuthPath())
	if err != nil {
		return nil, nil
	}

	var auth Auth
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, err
	}
	if auth.Refresh != "" {
		return &auth, nil
	}
	return nil, nil
}

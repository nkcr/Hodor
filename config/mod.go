package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config defines the structure of the configuration needed by Hodor.
type Config struct {
	// key is the release key, and value the target folder where the release
	// should be deployed.
	Entries map[string]string `json:"entries"`
}

// LoadFromJSON updates the config from the filepath.
func (c *Config) LoadFromJSON(filepath string) error {
	file, err := os.Open(filepath)
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}

	decoder := json.NewDecoder(file)

	err = decoder.Decode(c)
	if err != nil {
		return fmt.Errorf("failed to decode file: %v", err)
	}

	return nil
}

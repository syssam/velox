package main

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// configFile represents the .velox.yml configuration file.
type configFile struct {
	Schema   string   `yaml:"schema"`
	Target   string   `yaml:"target"`
	Package  string   `yaml:"package"`
	Features []string `yaml:"features"`
}

// loadConfigFile reads and parses a .velox.yml file at the given path.
// Returns nil, nil if the file does not exist.
func loadConfigFile(path string) (*configFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// findConfigFile walks up directories from dir looking for .velox.yml.
// Returns the path to the config file, or empty string if not found.
func findConfigFile(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}

	for {
		candidate := filepath.Join(abs, ".velox.yml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}

		parent := filepath.Dir(abs)
		if parent == abs {
			// Reached filesystem root.
			return ""
		}
		abs = parent
	}
}

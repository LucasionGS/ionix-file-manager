// Package config handles loading and saving user preferences for ifm.
// The configuration file is stored at:
//
//	$XDG_CONFIG_HOME/ifm/config.json   (Linux/BSD)
//	~/Library/Application Support/ifm/config.json  (macOS)
//	%AppData%\ifm\config.json          (Windows)
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const appName = "ifm"

// Config holds all persistent user preferences.
type Config struct {
	ShowDetails bool     `json:"show_details"`
	ShowHidden  bool     `json:"show_hidden"`
	Colors      Colors   `json:"colors,omitempty"`
	Favorites   []string `json:"favorites,omitempty"`
}

// Colors holds optional hex color overrides for the UI.
// Any field left empty uses the built-in default.
// Values should be CSS-style hex strings, e.g. "#ff8800".
type Colors struct {
	// Text colors
	Base      string `json:"base,omitempty"`      // main text
	Dim       string `json:"dim,omitempty"`       // subdued / secondary text
	Highlight string `json:"highlight,omitempty"` // accent color (active borders, title)
	Dir       string `json:"dir,omitempty"`       // directory entries
	Hidden    string `json:"hidden,omitempty"`    // hidden file entries
	Selected  string `json:"selected,omitempty"`  // selection highlight (sidebar cursor, menu)
	// UI chrome
	Border   string `json:"border,omitempty"`    // inactive pane borders
	StatusBg string `json:"status_bg,omitempty"` // status bar background
	CursorFg string `json:"cursor_fg,omitempty"` // text color on highlighted cursor rows
}

// configPath returns the path to the config file, creating the directory if needed.
func configPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, appName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the config file and returns the parsed Config.
// If the file does not exist, a default Config is returned without error.
func Load() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save writes cfg to the config file, creating it if necessary.
func Save(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

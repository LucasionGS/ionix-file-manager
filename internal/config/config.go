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

// Macro defines a user-configured shell command to run on files.
// In Command, the following variables are substituted before execution:
//   - $FILE   – absolute path of the focused/selected file (shell-quoted)
//   - $FILES  – space-separated shell-quoted list of all marked files (or $FILE if none marked)
//   - $DIR    – current working directory (shell-quoted)
//   - $NAME   – basename of the focused file (shell-quoted)
//   - $INPUT  – text entered by the user in the prompt (shell-quoted); triggers an input prompt
//
// Filter lists file extensions (e.g. ".png", ".jpg") that this macro applies to.
// An empty Filter means the macro is shown for all entries.
// Background runs the command without suspending the TUI (fire-and-forget).
type Macro struct {
	Name       string   `json:"name"`
	Command    string   `json:"command"`
	Filter     []string `json:"filter,omitempty"`
	Background bool     `json:"background,omitempty"`
}

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

// configDir returns the path to the ifm config directory, creating it if needed.
func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, appName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// configPath returns the path to the config file.
func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// macrosPath returns the path to the macros file.
func macrosPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "macros.json"), nil
}

// LoadMacros reads macros.json and returns the slice of Macro.
// If the file does not exist an empty slice is returned without error.
func LoadMacros() ([]Macro, error) {
	path, err := macrosPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var macros []Macro
	if err := json.Unmarshal(data, &macros); err != nil {
		return nil, err
	}
	return macros, nil
}

// SaveMacros writes macros to macros.json.
func SaveMacros(macros []Macro) error {
	path, err := macrosPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(macros, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
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

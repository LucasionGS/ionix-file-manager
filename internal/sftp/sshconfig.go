package sftp

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SSHHost represents a parsed Host block from ~/.ssh/config.
type SSHHost struct {
	Alias        string // the Host alias (e.g. "myserver")
	HostName     string // resolved hostname/IP
	User         string // SSH user
	Port         int    // port (default 22)
	IdentityFile string // path to key file, if specified
}

// ParseSSHConfig reads ~/.ssh/config and returns all non-wildcard Host blocks.
func ParseSSHConfig() []SSHHost {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return parseSSHConfigFile(filepath.Join(home, ".ssh", "config"))
}

func parseSSHConfigFile(path string) []SSHHost {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var hosts []SSHHost
	var current *SSHHost

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split into key and value.
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			// Try with = separator.
			parts = strings.SplitN(line, "=", 2)
		}
		if len(parts) < 2 {
			continue
		}

		keyword := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])

		switch keyword {
		case "host":
			// Skip wildcard patterns like * or 192.168.*
			if strings.Contains(value, "*") || strings.Contains(value, "?") {
				current = nil
				continue
			}
			hosts = append(hosts, SSHHost{
				Alias: value,
				Port:  22,
			})
			current = &hosts[len(hosts)-1]

		case "hostname":
			if current != nil {
				current.HostName = value
			}
		case "user":
			if current != nil {
				current.User = value
			}
		case "port":
			if current != nil {
				if p, err := strconv.Atoi(value); err == nil {
					current.Port = p
				}
			}
		case "identityfile":
			if current != nil {
				current.IdentityFile = value
			}
		}
	}

	// Filter out hosts with no resolving hostname.
	var filtered []SSHHost
	for _, h := range hosts {
		if h.HostName == "" {
			h.HostName = h.Alias // Host can be the hostname itself
		}
		filtered = append(filtered, h)
	}

	return filtered
}

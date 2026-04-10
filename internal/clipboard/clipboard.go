// Package clipboard writes text or image data to the system clipboard.
//
// Text strategy (tried in order):
//  1. wl-copy       — Wayland
//  2. xclip         — X11
//  3. OSC 52        — terminal escape sequence (works in kitty, tmux, etc.)
//
// Image strategy (tried in order):
//  1. wl-copy --type <mime>   — Wayland
//  2. xclip -t <mime>         — X11
//  (OSC 52 carries only text, so no image fallback via terminal)
package clipboard

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
)

// Write copies text to the system clipboard.
func Write(text string) error {
	data := []byte(text)
	if writeToCmd(data, "wl-copy") {
		return nil
	}
	if writeToCmd(data, "xclip", "-selection", "clipboard") {
		return nil
	}
	writeOSC52(text)
	return nil
}

// WriteImage copies the contents of an image file to the clipboard with the
// correct MIME type so applications can paste it as an image (not a path).
// mime should be e.g. "image/png" or "image/jpeg".
func WriteImage(path, mime string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if writeToCmd(data, "wl-copy", "--type", mime) {
		return nil
	}
	if writeToCmd(data, "xclip", "-selection", "clipboard", "-t", mime) {
		return nil
	}
	return fmt.Errorf("no clipboard tool available for image data (install wl-copy or xclip)")
}

// writeToCmd pipes data into the named command. Returns true on success.
func writeToCmd(data []byte, name string, args ...string) bool {
	path, err := exec.LookPath(name)
	if err != nil {
		return false
	}
	cmd := exec.Command(path, args...)
	cmd.Stdin = bytes.NewReader(data)
	return cmd.Run() == nil
}

// writeOSC52 sends the OSC 52 escape sequence to write text to the terminal clipboard.
func writeOSC52(text string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	fmt.Printf("\033]52;c;%s\007", encoded)
}

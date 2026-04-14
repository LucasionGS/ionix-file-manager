// Package kitty provides support for the Kitty terminal graphics protocol.
// https://sw.kovidgoyal.net/kitty/graphics-protocol/
package kitty

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// IsSupported returns true if the current terminal supports the kitty graphics protocol.
func IsSupported() bool {
	term := os.Getenv("TERM")
	kittyPid := os.Getenv("KITTY_PID")
	return strings.Contains(term, "kitty") || kittyPid != ""
}

// ClearAll returns the escape sequence that deletes every kitty image on screen.
func ClearAll() string {
	return "\033_Ga=d,d=A;\033\\"
}

// Encode returns the base64 encoding of data, ready to pass to Place.
// Call this once in a background goroutine; the result can be reused on every render.
func Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Place returns escape sequences that:
//  1. Save the cursor position
//  2. Move to (col, row) — both 1-based terminal coordinates
//  3. Transmit and display encoded (base64) image data within cols×rows cells
//  4. Restore the cursor position
//
// encoded must be the base64-encoded image bytes (from Encode).
// id identifies the image for later deletion. f=100 lets kitty detect the
// format automatically (PNG, JPEG, etc.).
func Place(encoded string, col, row, cols, rows, id int) string {
	opts := fmt.Sprintf("a=T,f=100,c=%d,r=%d,i=%d,q=2", cols, rows, id)

	var sb strings.Builder
	sb.WriteString("\033[s") // save cursor
	sb.WriteString(fmt.Sprintf("\033[%d;%dH", row, col))

	const chunkSize = 4096
	for i := 0; i < len(encoded); i += chunkSize {
		end := i + chunkSize
		more := 1
		if end >= len(encoded) {
			end = len(encoded)
			more = 0
		}
		chunk := encoded[i:end]
		if i == 0 {
			sb.WriteString(fmt.Sprintf("\033_G%s,m=%d;%s\033\\", opts, more, chunk))
		} else {
			sb.WriteString(fmt.Sprintf("\033_Gm=%d;%s\033\\", more, chunk))
		}
	}

	sb.WriteString("\033[u") // restore cursor
	return sb.String()
}

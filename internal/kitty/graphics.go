// Package kitty provides support for the Kitty terminal graphics protocol.
// https://sw.kovidgoyal.net/kitty/graphics-protocol/
package kitty

import (
	"fmt"
	"os"
	"strings"
)

// IsSupported returns true if the current terminal is Kitty.
func IsSupported() bool {
	term := os.Getenv("TERM")
	kittyPid := os.Getenv("KITTY_PID")
	return strings.Contains(term, "kitty") || kittyPid != ""
}

// ClearImage removes a previously displayed image at the given placement ID.
func ClearImage(placementID int) {
	fmt.Printf("\x1b_Ga=d,d=i,i=%d;\x1b\\", placementID)
}

// DisplayImageFile renders an image file using the kitty graphics protocol.
// x, y are the cell coordinates; w, h are the max cell dimensions (0 = auto).
func DisplayImageFile(path string, placementID, x, y, w, h int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return displayImageBytes(data, placementID, x, y, w, h)
}

func displayImageBytes(data []byte, placementID, x, y, w, h int) error {
	import64 := encodeBase64(data)

	// Build placement options
	opts := fmt.Sprintf("a=T,f=100,i=%d,p=%d", placementID, placementID)
	if x > 0 {
		opts += fmt.Sprintf(",X=%d", x)
	}
	if y > 0 {
		opts += fmt.Sprintf(",Y=%d", y)
	}
	if w > 0 {
		opts += fmt.Sprintf(",c=%d", w)
	}
	if h > 0 {
		opts += fmt.Sprintf(",r=%d", h)
	}

	// Chunk the payload (max 4096 bytes per chunk per spec)
	const chunkSize = 4096
	for i := 0; i < len(import64); i += chunkSize {
		end := i + chunkSize
		more := 1
		if end >= len(import64) {
			end = len(import64)
			more = 0
		}
		chunk := import64[i:end]

		var payload string
		if i == 0 {
			payload = fmt.Sprintf("\x1b_G%s,m=%d;%s\x1b\\", opts, more, chunk)
		} else {
			payload = fmt.Sprintf("\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
		fmt.Print(payload)
	}

	return nil
}

func encodeBase64(data []byte) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var sb strings.Builder
	for i := 0; i < len(data); i += 3 {
		remaining := len(data) - i
		b0 := data[i]
		var b1, b2 byte
		if remaining > 1 {
			b1 = data[i+1]
		}
		if remaining > 2 {
			b2 = data[i+2]
		}
		sb.WriteByte(chars[b0>>2])
		sb.WriteByte(chars[(b0&0x3)<<4|b1>>4])
		if remaining > 1 {
			sb.WriteByte(chars[(b1&0xf)<<2|b2>>6])
		} else {
			sb.WriteByte('=')
		}
		if remaining > 2 {
			sb.WriteByte(chars[b2&0x3f])
		} else {
			sb.WriteByte('=')
		}
	}
	return sb.String()
}

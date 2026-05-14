// Package blocklet runs the blocklet CLI (Rust: github.com/tanav-malhotra/blocklet).
package blocklet

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Render invokes blocklet. If font is non-empty it is passed as -f FONT; otherwise --no-shadow.
// If maxWidth > 0, passes -w for word-wrapped output width (blocklet-native).
func Render(ctx context.Context, bin, phrase, font string, maxWidth int) (string, error) {
	if bin == "" {
		bin = "blocklet"
	}
	var args []string
	if strings.TrimSpace(font) != "" {
		args = append(args, "-f", strings.TrimSpace(font))
	} else {
		args = append(args, "--no-shadow")
	}
	if maxWidth > 0 {
		args = append(args, "-w", strconv.Itoa(maxWidth))
	}
	args = append(args, phrase)

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%w (stderr: %s)", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

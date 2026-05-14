// Package blocklet runs the blocklet CLI (Rust: github.com/tanav-malhotra/blocklet).
package blocklet

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Render invokes blocklet with phrase as a single argument. If font is non-empty it is passed as -f FONT;
// otherwise --no-shadow is used (standard_solid, █ blocks without box-drawing shadow).
func Render(ctx context.Context, bin, phrase, font string) (string, error) {
	if bin == "" {
		bin = "blocklet"
	}
	var args []string
	if strings.TrimSpace(font) != "" {
		args = append(args, "-f", strings.TrimSpace(font))
	} else {
		args = append(args, "--no-shadow")
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

package transcode

import (
	"fmt"
	"io"
	"os/exec"
)

// feedStdin pumps in into the command's stdin on a goroutine the caller does
// NOT join. Assigning a non-file reader to cmd.Stdin makes exec own that copy
// and Wait block on it — and no WaitDelay rescues a copier parked in the
// source's Read (exec only closes its own pipes), so a stalled torrent read
// kept Run from returning after ffmpeg had already been killed. Through
// StdinPipe, Wait closes the pipe once the process exits and returns; the
// pump ends at the source's next EOF/error or on its next write.
func feedStdin(cmd *exec.Cmd, in io.Reader) error {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stdin pipe: %w", err)
	}
	go func() {
		_, _ = io.Copy(stdin, in)
		_ = stdin.Close()
	}()
	return nil
}

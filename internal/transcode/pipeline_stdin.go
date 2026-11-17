package transcode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"syscall"
)

// feedStdin pumps in into the command's stdin on a goroutine the caller does
// NOT join. Assigning a non-file reader to cmd.Stdin makes exec own that copy
// and Wait block on it — and no WaitDelay rescues a copier parked in the
// source's Read (exec only closes its own pipes), so a stalled torrent read
// kept Run from returning after ffmpeg had already been killed. Through
// StdinPipe, Wait closes the pipe once the process exits and returns; the
// pump ends at the source's next EOF/error or on its next write.
//
// Because the pump outlives Run, a caller whose source cannot take a Close
// concurrent with a Read must serialize the two itself.
func feedStdin(cmd *exec.Cmd, in io.Reader) error {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stdin pipe: %w", err)
	}
	go func() {
		_, err := io.Copy(stdin, in)
		_ = stdin.Close()
		// ffmpeg only sees a clean EOF here, so a real source failure would
		// otherwise end as a silently truncated stream.
		if !expectedPumpEnd(err) {
			log.Printf("transcode: source read failed mid-stream: %v", err)
		}
	}()
	return nil
}

// expectedPumpEnd reports whether the stdin pump stopped for a routine
// reason: source EOF (nil), ffmpeg gone (EPIPE / pipe closed by Wait), or the
// request canceled.
func expectedPumpEnd(err error) bool {
	return err == nil ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, os.ErrClosed) ||
		errors.Is(err, context.Canceled)
}

//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestIgnoredSIGPIPESurfacesEPIPE(t *testing.T) {
	if os.Getenv("OFGA_TEST_BROKEN_PIPE_HELPER") == "1" {
		ignoreBrokenPipeSignal()
		block := make([]byte, 64*1024)
		for {
			if _, err := os.Stdout.Write(block); err != nil {
				if errors.Is(err, syscall.EPIPE) {
					os.Exit(0)
				}
				os.Exit(2)
			}
		}
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestIgnoredSIGPIPESurfacesEPIPE$")
	cmd.Env = append(os.Environ(), "OFGA_TEST_BROKEN_PIPE_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("process should observe EPIPE rather than exit on SIGPIPE: %v", err)
	}
}

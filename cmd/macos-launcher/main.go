//go:build darwin

// The launcher retains its macOS privacy identity across proxy deployments.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--check" {
		file, err := os.Open(filepath.Join(os.Args[2], "config.yaml"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if errClose := file.Close(); errClose != nil {
			fmt.Fprintln(os.Stderr, errClose)
			os.Exit(1)
		}
		fmt.Println("Runtime access verified.")
		return
	}
	if len(os.Args) != 2 || !filepath.IsAbs(os.Args[1]) {
		fmt.Fprintln(os.Stderr, "Usage: cliproxyapi-service [--check] /absolute/runtime")
		os.Exit(2)
	}
	runtime := os.Args[1]
	child := exec.Command(filepath.Join(runtime, "current", "cli-proxy-api"), "--config", filepath.Join(runtime, "config.yaml"))
	child.Dir = runtime
	child.Env = append(os.Environ(), "MANAGEMENT_STATIC_PATH="+filepath.Join(runtime, "current", "management.html"))
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if errStart := child.Start(); errStart != nil {
		fmt.Fprintln(os.Stderr, errStart)
		os.Exit(1)
	}
	go func() {
		for sig := range signals {
			if errSignal := child.Process.Signal(sig); errSignal != nil && !errors.Is(errSignal, os.ErrProcessDone) {
				fmt.Fprintln(os.Stderr, errSignal)
			}
		}
	}()
	if errWait := child.Wait(); errWait != nil {
		var exit *exec.ExitError
		if errors.As(errWait, &exit) {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, errWait)
		os.Exit(1)
	}
}

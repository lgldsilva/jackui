package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

const healthcheckTimeout = 5 * time.Second

// handledHealthcheck runs the `healthcheck` subcommand used by the Docker
// HEALTHCHECK. It probes /healthz from inside the container with the binary
// itself, so the images don't depend on wget/curl (the nvidia base ships
// neither, which left prod with the healthcheck disabled).
func handledHealthcheck() bool {
	if len(os.Args) <= 1 || os.Args[1] != "healthcheck" {
		return false
	}
	if err := probeHealth(healthcheckURL(os.Getenv("JACKUI_PORT")), healthcheckTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
	}
	return true
}

func healthcheckURL(port string) string {
	if port == "" {
		port = "8989"
	}
	return "http://127.0.0.1:" + port + "/healthz"
}

func probeHealth(url string, timeout time.Duration) error {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url) //nolint:noctx // one-shot CLI probe bounded by client timeout
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}

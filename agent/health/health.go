// Package health provides functions to check and update the health of the agent.
// It uses a file in the temp directory to store the last confirmed connection timestamp.
// If the timestamp is older than 90 seconds, the agent is considered unhealthy.
// NB: The agent must be started with the Start() method to be considered healthy.
package health

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Health probes must only read the timestamp. Initializing it here would let
// every invocation of `agent health` make an offline agent appear healthy.
var healthFile = filepath.Join(os.TempDir(), "vpsmonitor_health")

func updateHealthFile(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	return file.Close()
}

// Check checks if the agent is connected by checking the modification time of the health file
func Check() error {
	fileInfo, err := os.Stat(healthFile)
	if err != nil {
		return err
	}
	if time.Since(fileInfo.ModTime()) > 91*time.Second {
		log.Println("over 90 seconds since last connection")
		return errors.New("unhealthy")
	}
	return nil
}

// Update updates the modification time of the health file
func Update() error {
	return updateHealthFile(healthFile)
}

// CleanUp removes the health file
func CleanUp() error {
	err := os.Remove(healthFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

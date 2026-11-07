//go:build !linux

package agent

import "github.com/CodeAIX/vpsmonitor/internal/entities/smart"

func scanMdraidDevices() []string {
	return nil
}

func readMdraidSmartData(path string) (string, *smart.SmartData, bool) {
	return "", nil, false
}

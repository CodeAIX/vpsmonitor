//go:build !linux

package agent

import (
	"github.com/CodeAIX/vpsmonitor/internal/accounting"
	"github.com/CodeAIX/vpsmonitor/internal/entities/traffic"
)

func readNativeTraffic() (accounting.Reading, string) {
	return accounting.Reading{}, traffic.UnsupportedPlatform
}

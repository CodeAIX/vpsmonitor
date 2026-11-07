//go:build !linux && !windows && !darwin

package wifi

import (
	"context"

	"github.com/CodeAIX/vpsmonitor/internal/entities/system"
)

func collect(context.Context) map[string]system.WiFi { return nil }

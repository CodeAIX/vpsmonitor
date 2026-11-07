//go:build !linux

package agent

func trafficPlatformSupported() bool { return false }

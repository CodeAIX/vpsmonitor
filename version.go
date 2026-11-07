package vpsmonitor

// Product identity is independent of the inherited internal capability baseline.
const IsVPSMonitor = true

var BuildVersion = "0.1.0-dev"
var BuildCommit = "unknown"
var BuildImageTag = ""

// Published images enforce Tailnet transport. Untagged source builds are also
// used by the local test harness, which supplies its own loopback endpoints.
func RequiresTailnet() bool { return BuildImageTag != "" }

import { expect, test } from "bun:test"
import { agentCompose, sourceRef } from "../src/lib/deployment"

test("Docker installation contains native host accounting and persistence without vnStat setup", () => {
	const commit = "a".repeat(40)
	const yaml = agentCompose("public-key", "test-token", "https://monitor.example.com", commit)
	expect(yaml).toContain(`#${commit}`)
	expect(yaml).toContain("network_mode: host")
	expect(yaml).toContain("VPSMONITOR_NETWORK_SCOPE: host")
	expect(yaml).toContain("/var/lib/vpsmonitor-agent")
	expect(yaml).toContain('DISABLE_SSH: "true"')
	expect(yaml).not.toContain("vnstat")
})

test("source refs cannot inject build arguments", () => {
	expect(sourceRef("main; touch /tmp/test")).toBe("main")
	expect(agentCompose("key", "token", "https://monitor.example.com", "bad ref")).toContain("#main\n")
})

test("Compose keeps a literal multiline credential", () => {
	const yaml = agentCompose("key", "line\nsecond", "https://monitor.example.com", "unknown")
	expect(yaml).toContain('TOKEN: "line\\nsecond"')
})

test("Compose interpolation cannot substitute a secret value from the operator environment", () => {
 const yaml = agentCompose("key", "${PRIVATE_VALUE}", "https://monitor.example.com", "unknown")
 expect(yaml).toContain('TOKEN: "$${PRIVATE_VALUE}"')
})

test("a release Hub generates pull-only Agent instructions pinned to its matching tag", () => {
	const args = ["key", "token", "http://100.65.1.2:8091", "unknown", "1.0.0", "v1.0.0"] as const
	const yaml = agentCompose(...args)
	expect(yaml).toContain("image: ghcr.io/codeaix/vpsmonitor-agent:v1.0.0")
	expect(yaml).toContain("container_name: ${VPSMONITOR_AGENT_CONTAINER_NAME:-vpsmonitor-agent}")
	expect(yaml).toContain('HUB_URL: "http://100.65.1.2:8091"')
	expect(yaml).toContain("./agent_data:/var/lib/vpsmonitor-agent")
	expect(yaml).toContain("network_mode: host")
	expect(yaml).not.toContain("build:")
	const malformed = agentCompose("key", "token", "https://monitor.example.com", "unknown", "0.1.0-beta.2", "v0.1.0; echo injected")
	expect(malformed).not.toContain("echo injected")
})

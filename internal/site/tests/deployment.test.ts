import { expect, test } from "bun:test"
import { agentCompose, agentRun, sourceRef } from "../src/lib/deployment"

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
	expect(agentRun("key", "token", "https://monitor.example.com", "bad ref")).toContain("#main'")
})

test("copy commands quote a literal token rather than executable shell syntax", () => {
	const token = "token'$(printf dangerous)"
	const command = agentRun("key", token, "https://monitor.example.com", "unknown")
	expect(command).toContain(`TOKEN='token'"'"'$(printf dangerous)'`)
	const yaml = agentCompose("key", "line\nsecond", "https://monitor.example.com", "unknown")
	expect(yaml).toContain('TOKEN: "line\\nsecond"')
})

test("Compose interpolation cannot substitute a secret value from the operator environment", () => {
 const yaml = agentCompose("key", "${PRIVATE_VALUE}", "https://monitor.example.com", "unknown")
 expect(yaml).toContain('TOKEN: "$${PRIVATE_VALUE}"')
})

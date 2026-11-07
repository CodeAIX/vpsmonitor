import { describe, expect, mock, test } from "bun:test"

mock.module("../src/lib/api", () => ({
	pb: { authStore: { token: "unused-test-session" }, send: () => Promise.resolve({}) },
}))
const { createTrafficAPI } = await import("../src/lib/traffic-api")

describe("traffic request cache", () => {
	test("uses bounded batch pages rather than one request per system", async () => {
		const offsets: number[] = []
		const api = createTrafficAPI(async <T>(_path: string, options: any) => {
			const offset = options.query.offset
			offsets.push(offset)
			return { items: Array.from({ length: offset ? 1 : 50 }, (_, i) => ({ system: `s${offset + i}` })), has_more: offset === 0 } as T
		}, () => "test-user")
		const [a, b] = await Promise.all([api.overview(), api.overview()])
		expect(a.items).toHaveLength(51)
		expect(b.items).toHaveLength(51)
		expect(offsets).toEqual([0, 50])
	})
	test("deduplicates concurrent overview requests and expires after 60 seconds", async () => {
		let calls = 0,
			now = 0
		const api = createTrafficAPI(
			async <T>() => {
				calls++
				return { items: [] } as T
			},
			() => "test-user",
			() => now
		)
		await Promise.all([api.overview(), api.overview(), api.overview()])
		expect(calls).toBe(1)
		now = 30_000
		await api.overview()
		expect(calls).toBe(1)
		now = 61_000
		await api.overview()
		expect(calls).toBe(2)
	})
	test("does not reuse cached or in-flight data across sessions", async () => {
		let session = "test-a",
			calls = 0
		let release!: (value: unknown) => void
		const api = createTrafficAPI(
			<T>() => {
				calls++
				return calls === 1
					? (new Promise((resolve) => {
							release = resolve
						}) as Promise<T>)
					: Promise.resolve({ items: [] } as T)
			},
			() => session
		)
		const old = api.overview().catch((error: Error) => error.message)
		session = "test-b"
		await api.overview()
		release({ items: ["old-private-data"] })
		expect(await old).toBe("Traffic request superseded")
		expect(calls).toBe(2)
		await api.overview()
		expect(calls).toBe(2)
	})
	test("failed requests can retry immediately", async () => {
		let calls = 0
		const api = createTrafficAPI(
			async <T>() => {
				if (++calls === 1) throw new Error("offline")
				return { items: [] } as T
			},
			() => "test-user"
		)
		await expect(api.overview()).rejects.toThrow("offline")
		await api.overview()
		expect(calls).toBe(2)
	})
	test("settings invalidate old results and details use structured query parameters", async () => {
		const requests: { path: string; options: any }[] = []
		const api = createTrafficAPI(
			async <T>(path: string, options: any) => {
				requests.push({ path, options })
				return { items: [], has_more: false } as T
			},
			() => "test-user"
		)
		await api.overview()
		await api.settings("s1", { iface: "eth0", count_mode: "tx" })
		await api.overview()
		await api.detail("s1&system=foreign", "2026-11-01", "2026-11-07")
		expect(requests.filter((r) => r.path.endsWith("overview"))).toHaveLength(2)
		expect(requests[1].options.method).toBe("PATCH")
		expect(requests.at(-1)?.options.query.system).toBe("s1&system=foreign")
	})
})

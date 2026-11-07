import { describe, expect, mock, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import {
	trafficBytes,
	trafficProgress,
	trafficQuota,
	trafficQuotaInput,
	trafficRange,
	trafficSplit,
} from "../src/lib/traffic-display"
import type { TrafficView } from "../src/types"

mock.module("@lingui/core/macro", () => ({ t: (strings: TemplateStringsArray) => strings.join("") }))
mock.module("@/lib/utils", () => ({ decimalString: (value: number, digits: number) => value.toFixed(digits) }))
const { TrafficUsageCell } = await import("../src/components/traffic/traffic-usage-cell")

describe("traffic date and byte display", () => {
	test("keeps UTC date boundaries across local offsets and leap days", () => {
		expect(trafficRange(1, new Date("2026-11-06T00:30:00+08:00"))).toEqual({ from: "2026-11-05", to: "2026-11-06" })
		expect(trafficRange(2, new Date("2028-03-01T12:00:00Z"))).toEqual({ from: "2028-02-29", to: "2028-03-02" })
	})
	test("distinguishes SI, binary, missing, zero and unsafe values", () => {
		expect(trafficBytes(1e12)).toBe("1 TB")
		expect(trafficBytes(1024 ** 3, "GiB")).toBe("1 GiB")
		expect(trafficBytes(null)).toBe("—")
		expect(trafficBytes(0)).toBe("0 B")
		expect(trafficBytes(Number.MAX_SAFE_INTEGER + 1)).toBe("—")
	})
	test("round trips quotas exactly through GB and TB without float rounding", () => {
		for (const bytes of [0, 1, 1e12, 1073741824, Number.MAX_SAFE_INTEGER]) {
			for (const factor of [1e9, 1e12]) expect(trafficQuota(trafficQuotaInput(bytes, factor), factor)).toBe(bytes)
		}
		expect(trafficQuota("0.000000001", 1e9)).toBe(1)
		for (const invalid of ["-1", "NaN", "Infinity", "1e3", "0.0000000001", "9007199.254740992"])
			expect(() => trafficQuota(invalid, 1e9)).toThrow()
	})
})

test("over-quota display retains 120% but clamps the actual progress bar", () => {
	const item = {
		used_bytes: 1200,
		quota_bytes: 1000,
		percent: 120,
		status: "ok",
		freshness: "fresh",
		coverage: "unknown",
		quality: "normal",
		system_status: "up",
		count_mode: "tx",
		settings: { display_unit: "GB" },
	} as TrafficView
	const html = renderToStaticMarkup(<TrafficUsageCell item={item} />)
	expect(html).toContain("120.0%")
	expect(html).toContain('aria-valuenow="100"')
	expect(html).toContain("width:100%")
	expect(html).not.toContain("width:120%")
	expect(trafficProgress(null)).toBeNull()
	expect(trafficProgress(0)).toBe(0)
	const missing = renderToStaticMarkup(<TrafficUsageCell item={{ ...item, used_bytes: null, status: "not_enabled" }} />)
	expect(missing).toContain("Traffic not enabled")
	expect(missing).not.toContain("progressbar")
})

describe("compact traffic meter", () => {
	const item = {
		used_bytes: 120,
		rx_bytes: 30,
		tx_bytes: 90,
		quota_bytes: 100,
		percent: 120,
		status: "ok",
		freshness: "fresh",
		coverage: "partial",
		quality: "normal",
		system_status: "down",
		count_mode: "rx_tx",
		settings: { enabled: true, display_unit: "GB" },
		period_start: "2026-10-15",
		period_end_exclusive: "2026-11-15",
		last_success_at: "2026-11-06T05:00:00Z",
	} as TrafficView

	test("keeps both direction shares when over quota, with details confined to the tooltip", () => {
		const html = renderToStaticMarkup(<TrafficUsageCell compact item={item} />)
		expect(html).toContain('aria-valuenow="100"')
		expect(html).toContain("width:100%")
		expect(html).toContain("width:25%")
		expect(html).toContain("width:75%")
		expect(html).toContain("bg-chart-2")
		expect(html).toContain("bg-chart-1")
		expect(html).toContain("2026-10-15 → 2026-11-15")
		expect(html).toContain("Offline · last known")
		expect(html.replace(/<[^>]*>/g, "")).toBe("120.0%")
	})

	test("shows only outbound usage for tx billing and preserves null versus zero", () => {
		expect(trafficSplit(600, 200, "rx_tx")).toEqual({ rx: 75, tx: 25 })
		expect(trafficSplit(600, 200, "tx")).toEqual({ rx: 0, tx: 100 })
		expect(trafficSplit(Number.MAX_SAFE_INTEGER, 1, "rx_tx")).toBeNull()
		const tx = renderToStaticMarkup(<TrafficUsageCell compact item={{ ...item, count_mode: "tx" }} />)
		expect(tx).not.toContain("bg-chart-2")
		expect(tx).toContain("bg-chart-1")
		const zero = renderToStaticMarkup(
			<TrafficUsageCell compact item={{ ...item, rx_bytes: 0, tx_bytes: 0, used_bytes: 0, percent: 0 }} />
		)
		expect(zero).toContain('aria-valuenow="0"')
		expect(zero.replace(/<[^>]*>/g, "")).toBe("0.00%")
		for (const data of [
			undefined,
			{ ...item, used_bytes: null },
			{ ...item, quota_bytes: 0, percent: null },
			{ ...item, settings: { ...item.settings, enabled: false } },
		]) {
			const html = renderToStaticMarkup(<TrafficUsageCell compact item={data} />)
			expect(html.replace(/<[^>]*>/g, "")).toBe("—")
			expect(html).not.toContain('role="progressbar"')
		}
	})
})

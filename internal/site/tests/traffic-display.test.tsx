import { afterEach, beforeEach, describe, expect, mock, spyOn, test } from "bun:test"
import { renderToStaticMarkup } from "react-dom/server"
import {
	billingDaysRemaining,
	trafficBytes,
	trafficProgress,
	trafficQuota,
	trafficQuotaInput,
	trafficRange,
} from "../src/lib/traffic-display"
import type { TrafficView } from "../src/types"

mock.module("@lingui/core/macro", () => ({
	t: (strings: TemplateStringsArray, ...values: unknown[]) =>
		strings.reduce((text, part, i) => text + part + (values[i] ?? ""), ""),
}))
mock.module("@/lib/utils", () => ({
	cn: (...classes: string[]) => classes.filter(Boolean).join(" "),
	decimalString: (value: number, digits: number) => value.toFixed(digits),
}))
const { TrafficUsageCell } = await import("../src/components/traffic/traffic-usage-cell")

describe("traffic date and byte display", () => {
	test("counts down to the exclusive UTC billing boundary", () => {
		expect(billingDaysRemaining("2028-03-01", Date.parse("2028-02-28T23:59:00Z"))).toBe(2)
		expect(billingDaysRemaining("2028-03-01", Date.parse("2028-02-29T08:00:00+08:00"))).toBe(1)
		expect(billingDaysRemaining("2028-03-01", Date.parse("2028-02-29T23:59:00Z"))).toBe(0)
		expect(billingDaysRemaining("2028-03-01", Date.parse("2028-03-01T00:00:00Z"))).toBeNull()
	})
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
	beforeEach(() => spyOn(Date, "now").mockReturnValue(Date.parse("2026-11-06T00:00:00Z")))
	afterEach(() => mock.restore())
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

	test("shows one rounded meter and keeps coverage out of the visible cell", () => {
		const html = renderToStaticMarkup(<TrafficUsageCell compact item={item} href="/system/example" />)
		expect(html).toContain('aria-valuenow="100"')
		expect(html).toContain("width:100%")
		expect(html).toContain("rounded-full bg-green-500")
		expect(html).toContain("2026-10-15 → 2026-11-15")
		expect(html).toContain("Offline · last known")
		expect(html).toContain('href="/system/example"')
		expect(html).not.toContain('title="')
		expect(html.replace(/<[^>]*>/g, "")).toBe("120.0%9 days left")
	})

	test("shows only outbound usage for tx billing and preserves null versus zero", () => {
		const tx = renderToStaticMarkup(
			<TrafficUsageCell compact item={{ ...item, count_mode: "tx", used_bytes: 90, percent: 90 }} />
		)
		expect(tx).toContain('aria-valuenow="90"')
		expect(tx).toContain("Outbound only")
		const zero = renderToStaticMarkup(
			<TrafficUsageCell compact item={{ ...item, rx_bytes: 0, tx_bytes: 0, used_bytes: 0, percent: 0 }} />
		)
		expect(zero).toContain('aria-valuenow="0"')
		expect(zero.replace(/<[^>]*>/g, "")).toStartWith("0.00%")
		for (const data of [
			undefined,
			{ ...item, used_bytes: null },
			{ ...item, quota_bytes: 0, percent: null },
			{ ...item, settings: { ...item.settings, enabled: false } },
		]) {
			const html = renderToStaticMarkup(<TrafficUsageCell compact item={data} />)
			expect(html.replace(/<[^>]*>/g, "")).toStartWith("—")
			expect(html).not.toContain('role="progressbar"')
		}
	})
})

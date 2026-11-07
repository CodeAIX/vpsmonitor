/** Date-only values stay UTC strings; never format them through local Date. */
export function trafficRange(days: number, now = new Date()) {
	const today = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate())
	return {
		from: new Date(today - (days - 1) * 86_400_000).toISOString().slice(0, 10),
		to: new Date(today + 86_400_000).toISOString().slice(0, 10),
	}
}

export function trafficBytes(value: number | null | undefined, unit: "GB" | "GiB" = "GB") {
	if (value == null || !Number.isSafeInteger(value) || value < 0) return "—"
	const base = unit === "GiB" ? 1024 : 1000
	const names = unit === "GiB" ? ["B", "KiB", "MiB", "GiB", "TiB", "PiB"] : ["B", "KB", "MB", "GB", "TB", "PB"]
	const index = value === 0 ? 0 : Math.min(Math.floor(Math.log(value) / Math.log(base)), names.length - 1)
	return `${(value / base ** index).toLocaleString(undefined, { maximumFractionDigits: 2 })} ${names[index]}`
}

export function trafficProgress(value: number | null | undefined) {
	return value == null || !Number.isFinite(value) ? null : Math.min(100, Math.max(0, value))
}

/** Shares of the used fill, so exceeding quota preserves the Rx/Tx ratio. */
export function trafficSplit(rx: number | null, tx: number | null, mode: "tx" | "rx_tx") {
	if (tx == null || !Number.isSafeInteger(tx) || tx < 0) return null
	if (mode === "tx") return { rx: 0, tx: 100 }
	if (rx == null || !Number.isSafeInteger(rx) || rx < 0 || !Number.isSafeInteger(rx + tx)) return null
	if (rx + tx === 0) return { rx: 0, tx: 0 }
	const received = (rx / (rx + tx)) * 100
	return { rx: received, tx: 100 - received }
}

export function trafficQuota(input: string, factor: number) {
	const places = factor === 1e9 ? 9 : factor === 1e12 ? 12 : -1
	const value = input.trim() || "0"
	if (places < 0 || !/^\d+(\.\d*)?$/.test(value)) throw new Error("Invalid quota")
	const [whole, fraction = ""] = value.split(".")
	if (fraction.length > places) throw new Error("Quota must use whole bytes")
	const bytes = BigInt(whole) * BigInt(factor) + BigInt(fraction.padEnd(places, "0") || "0")
	if (bytes > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error("Quota is too large")
	return Number(bytes)
}

export function trafficQuotaInput(bytes: number, factor: number) {
	const places = factor === 1e12 ? 12 : 9
	const value = BigInt(bytes),
		base = BigInt(factor)
	const fraction = (value % base).toString().padStart(places, "0").replace(/0+$/, "")
	return `${value / base}${fraction ? `.${fraction}` : ""}`
}

export function trafficTimestamp(value: string | null | undefined) {
	if (!value) return "—"
	const date = new Date(value)
	return Number.isNaN(date.getTime()) ? "—" : `${date.toISOString().slice(0, 19).replace("T", " ")} UTC`
}

import type { HistoryPoint } from "./history-api"

/** Only history-covered buckets are replaced. Partial recovered buckets fill
 * gaps but never displace an existing live/official aggregate. No zero samples
 * or synthetic live state are inserted for missing minutes. */
export function mergeHistory<T extends { created: string | number | null }>(
	normal: T[],
	history: HistoryPoint[],
	containers = false
): T[] {
	if (!history.length) return normal
	const rows = normal.filter((row) => row.created !== null)
	const replacements = new Map<number, HistoryPoint>()
	for (const point of history) {
		if (containers && !point.containers?.length) continue
		const hasNormal = rows.some(
			(row) => Math.floor(new Date(row.created!).getTime() / point.interval) * point.interval === point.bucket
		)
		// Keep the Hub's real-time minute sample when it exists. Larger complete
		// buckets can correct aggregates that were formed around an outage.
		if ((point.complete && point.interval > 60_000) || !hasNormal) replacements.set(point.bucket, point)
	}
	if (!replacements.size) return normal
	const interval = history[0].interval
	return [
		...rows.filter((row) => !replacements.has(Math.floor(new Date(row.created!).getTime() / interval) * interval)),
		...Array.from(
			replacements.values(),
			(p) => ({ created: p.created, stats: containers ? p.containers : p.stats }) as unknown as T
		),
	].sort((a, b) => new Date(a.created!).getTime() - new Date(b.created!).getTime())
}

import { pb } from "@/lib/api"
import { chartTimeData } from "@/lib/utils"
import type { ChartTimes } from "@/types"

export type HistoryState = {
	supported: boolean
	retention_seconds: number
	cursor: number
	oldest: number
	newest: number
	last_sync: number
	cache_samples: number
	limited: boolean
	error: string
	gap_from: number
	gap_to: number
}
export type HistoryPoint = {
	created: number
	bucket: number
	interval: number
	samples: number
	complete: boolean
	stats: unknown
	containers: unknown[] | null
}
export type HistorySeries = { state: HistoryState | null; points: HistoryPoint[] }

// Share one bounded chart query between system, Docker and status consumers.
// Refresh old buckets as catch-up advances, rather than only appending new data.
const requests = new Map<string, Promise<HistorySeries>>()
const failedCharts = new Map<string, boolean>()
export function historyChartFailed(system: string) {
	return failedCharts.get(system) ?? false
}
export function getHistorySeries(system: string, chartTime: ChartTimes): Promise<HistorySeries> {
	const key = `${system}:${chartTime}:${Math.floor(Date.now() / 15_000)}`
	const ownerKey = `${pb.authStore.record?.id ?? ""}:${key}`
	const cached = requests.get(ownerKey)
	if (cached) return cached
	const now = new Date()
	const promise = pb.send<HistorySeries>("/api/vpsmonitor/history/stats", {
		query: {
			system,
			type: chartTimeData[chartTime].type,
			from: chartTimeData[chartTime].getOffset(now).getTime(),
			to: now.getTime() + 1,
		},
		requestKey: ownerKey,
	})
	requests.set(ownerKey, promise)
	if (requests.size > 24) {
		const first = requests.keys().next().value
		if (first) requests.delete(first)
	}
	promise.then(
		() => failedCharts.set(system, false),
		() => {
			requests.delete(ownerKey)
			failedCharts.set(system, true)
		}
	)
	if (failedCharts.size > 24) {
		const first = failedCharts.keys().next().value
		if (first) failedCharts.delete(first)
	}
	return promise
}

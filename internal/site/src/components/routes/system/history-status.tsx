import { Trans } from "@lingui/react/macro"
import { useEffect, useState } from "react"
import { historyChartFailed, type HistoryState } from "@/lib/history-api"
import { pb } from "@/lib/api"

export default function HistoryStatus({ systemId, online }: { systemId: string; online: boolean }) {
	const [state, setState] = useState<HistoryState | null | undefined>()
	const [failed, setFailed] = useState(false)
	useEffect(() => {
		let active = true
		setState(undefined)
		async function load() {
			try {
				const result = await pb.send<{ state: HistoryState | null }>("/api/vpsmonitor/history/status", {
					query: { system: systemId },
					requestKey: `history-status:${systemId}`,
				})
				if (active) {
					setState(result.state)
					setFailed(historyChartFailed(systemId))
				}
			} catch {
				if (active) setFailed(true)
			}
		}
		load()
		const timer = setInterval(load, 15_000)
		return () => {
			active = false
			clearInterval(timer)
		}
	}, [systemId])
	if (failed)
		return (
			<p className="text-sm text-muted-foreground">
				<Trans>Cached history could not be loaded. Live monitoring remains available.</Trans>
			</p>
		)
	if (state === undefined) return null
	if (!state?.supported)
		return (
			<p className="text-sm text-muted-foreground">
				<Trans>
					Offline history is unavailable on this Agent.
				</Trans>
			</p>
		)
	const days = Math.floor(state.retention_seconds / 86_400)
	return (
		<div className="flex flex-wrap gap-x-4 gap-y-1 text-sm text-muted-foreground" aria-live="polite">
			<span>
				<Trans>Agent offline cache: up to {days} days, one sample per minute.</Trans>
			</span>
			<span>
				{!online ? (
					<Trans>Agent disconnected; showing the last synced cache.</Trans>
				) : state.cache_samples === 0 ? (
					<Trans>Waiting for the first persisted sample.</Trans>
				) : state.cursor < state.newest ? (
					<Trans>Syncing cached samples…</Trans>
				) : (
					<Trans>Cached samples synced.</Trans>
				)}
			</span>
			{state.oldest > 0 && (
				<span>
					<Trans>Available since</Trans>: {new Date(state.oldest).toLocaleString()}
				</span>
			)}
			{state.last_sync > 0 && (
				<span>
					<Trans>Last sync</Trans>: {new Date(state.last_sync).toLocaleString()}
				</span>
			)}
			{(state.limited || state.error || state.gap_to > 0) && (
				<span className="text-amber-600 dark:text-amber-400">
					<Trans>
						Some history is unavailable because of a cache limit, storage error or an expired retention window. Missing
						samples are not treated as zero.
					</Trans>
				</span>
			)}
		</div>
	)
}

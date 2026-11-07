import { t } from "@lingui/core/macro"
import type { TrafficView } from "@/types"
import { billingDaysRemaining, trafficBytes, trafficProgress, trafficTimestamp } from "@/lib/traffic-display"
import { decimalString } from "@/lib/utils"

export function trafficStatusLabel(status: string) {
	switch (status) {
		case "ok":
			return t`Collected`
		case "unconfigured":
			return t`Choose an interface`
		case "disabled":
			return t`Disabled`
		case "interface_required":
			return t`Select the host uplink interface`
		case "host_network_required":
			return t`Agent requires host networking`
		case "clock_error":
			return t`Host clock moved backwards`
		case "storage_error":
			return t`Traffic storage needs attention`
		case "vnstat_missing":
			return t`vnStat not installed`
		case "unsupported_timebase":
			return t`Verify UTC configuration`
		case "not_enabled":
			return t`Traffic not enabled`
		case "unsupported_platform":
			return t`Unsupported platform`
		case "initializing":
			return t`Initializing`
		case "no_database":
			return t`vnStat database unavailable`
		case "no_interfaces":
			return t`No monitored interfaces`
		case "permission_denied":
			return t`vnStat permission denied`
		case "exec_timeout":
			return t`vnStat query timed out`
		default:
			return t`Collection needs attention`
	}
}

export function TrafficUsageCell({
	item,
	loading,
	error,
	compact = false,
}: {
	item?: TrafficView
	loading?: boolean
	error?: boolean
	compact?: boolean
}) {
	if (!item)
		return compact ? (
			<CompactTrafficMeter description={error ? t`Traffic unavailable` : loading ? t`Loading…` : "—"} />
		) : (
			<span className="text-xs text-muted-foreground">
				{error ? t`Traffic unavailable` : loading ? t`Loading…` : "—"}
			</span>
		)
	const width = trafficProgress(item.percent)
	const unit = item.settings.display_unit
	const warning =
		error ||
		item.status !== "ok" ||
		item.system_status !== "up" ||
		item.freshness === "stale" ||
		item.coverage !== "complete" ||
		item.quality === "suspected_reset"
	const label = error
		? t`Update failed`
		: item.status !== "ok"
			? trafficStatusLabel(item.status)
			: item.quality === "suspected_reset"
				? t`Suspected counter reset`
				: item.system_status === "down"
					? t`Offline · last known`
					: item.system_status === "paused"
						? t`Paused · last known`
						: item.freshness === "stale"
							? t`Stale`
							: item.coverage === "partial"
								? t`Incomplete period`
								: t`Coverage unverified`
	const description = `${item.period_start} → ${item.period_end_exclusive} UTC · ${item.count_mode === "tx" ? t`Outbound only` : t`Received + sent`} · ${trafficTimestamp(item.last_success_at)} · ${item.error_code || label}`
	if (compact) {
		const percent =
			item.used_bytes != null && item.quota_bytes > 0 && item.settings.enabled !== false ? item.percent : null
		const tooltip = [
			`${t`Traffic`}: ${trafficBytes(item.used_bytes, unit)} / ${item.quota_bytes > 0 ? trafficBytes(item.quota_bytes, unit) : t`No quota`}`,
			`${t`Received`} (Rx): ${trafficBytes(item.rx_bytes, unit)} · ${t`Sent`} (Tx): ${trafficBytes(item.tx_bytes, unit)}`,
			`${item.period_start} → ${item.period_end_exclusive} UTC ${t`(end exclusive, UTC)`}`,
			item.count_mode === "tx" ? t`Outbound only` : t`Received + sent`,
			`${t`Last sync`}: ${trafficTimestamp(item.last_success_at)}`,
			warning ? label : trafficStatusLabel(item.status),
			item.error_code,
		]
			.filter(Boolean)
			.join("\n")
		const days = billingDaysRemaining(item.period_end_exclusive)
		const countdown =
			item.settings.enabled === false || !item.period_end_exclusive
				? undefined
				: days == null
					? t`Refreshing billing period`
					: days === 0
						? t`Less than 1 day left`
						: days === 1
							? t`1 day left`
							: t`${days} days left`
		return <CompactTrafficMeter description={tooltip} percent={percent} countdown={countdown} />
	}
	return (
		<div className="min-w-0 max-w-52 text-xs" title={description}>
			{item.used_bytes == null ? (
				<span className="text-muted-foreground">{trafficStatusLabel(item.status)}</span>
			) : (
				<>
					<div className="flex flex-wrap gap-x-1 tabular-nums">
						<span>{trafficBytes(item.used_bytes, unit)}</span>
						{item.quota_bytes > 0 && (
							<span className="text-muted-foreground">/ {trafficBytes(item.quota_bytes, unit)}</span>
						)}
						{item.percent != null && <span>({item.percent.toFixed(1)}%)</span>}
					</div>
					{width != null && (
						<div
							role="progressbar"
							aria-label={t`Traffic quota usage`}
							aria-valuenow={width}
							aria-valuemin={0}
							aria-valuemax={100}
							aria-valuetext={`${item.percent?.toFixed(1)}%`}
							className="mt-1 h-1 overflow-hidden rounded bg-muted"
						>
							<div
								className={item.percent! >= 100 ? "h-full bg-red-500" : "h-full bg-primary"}
								style={{ width: `${width}%` }}
							/>
						</div>
					)}
					{warning && <div className="mt-0.5 text-amber-600 dark:text-amber-400">{label}</div>}
				</>
			)}
		</div>
	)
}

function CompactTrafficMeter({
	description,
	percent,
	countdown,
}: {
	description: string
	percent?: number | null
	countdown?: string
}) {
	const width = trafficProgress(percent)
	const value = width == null ? "—" : `${decimalString(percent!, percent! >= 10 ? 1 : 2)}%`
	return (
		<div
			className="flex gap-2 items-center tabular-nums tracking-tight w-full min-w-0"
			title={description}
			aria-label={description}
		>
			<span className={`min-w-8 shrink-0${percent != null && percent >= 100 ? " text-red-500" : ""}`}>{value}</span>
			<span
				className="relative flex-1 min-w-8 bg-muted h-[1em] rounded-full overflow-hidden"
				role={width == null ? undefined : "progressbar"}
				aria-label={width == null ? undefined : t`Traffic quota usage`}
				aria-valuenow={width ?? undefined}
				aria-valuemin={width == null ? undefined : 0}
				aria-valuemax={width == null ? undefined : 100}
				aria-valuetext={width == null ? undefined : value}
			>
				{width != null && (
					<span className="block h-full rounded-full bg-green-500" style={{ width: `${width}%` }} aria-hidden="true" />
				)}
				{countdown && (
					<span className="pointer-events-none absolute inset-x-1 top-1/2 -translate-y-1/2 truncate text-center text-[0.6875rem] leading-none text-zinc-700 dark:text-zinc-200 dark:[text-shadow:0_1px_2px_#000]">
						{countdown}
					</span>
				)}
			</span>
		</div>
	)
}

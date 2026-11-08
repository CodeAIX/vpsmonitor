import { t } from "@lingui/core/macro"
import { Link } from "@/components/router"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
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
	href,
}: {
	item?: TrafficView
	loading?: boolean
	error?: boolean
	compact?: boolean
	href?: string
}) {
	if (!item)
		return compact ? (
			<CompactTrafficMeter href={href} description={error ? t`Traffic unavailable` : loading ? t`Loading…` : "—"} />
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
		const meter = <CompactTrafficMeter href={href} description={description} percent={percent} countdown={countdown} />
		const countMode = item.count_mode === "tx" ? t`Outbound only` : t`Received + sent`
		return (
			<Tooltip delayDuration={200}>
				<TooltipTrigger asChild>{meter}</TooltipTrigger>
				<TooltipContent
					sideOffset={7}
					collisionPadding={12}
					className="w-72 max-w-[calc(100vw-24px)] rounded-xl p-3 text-start text-xs shadow-lg"
				>
					<div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
						<span className="font-semibold">{t`Traffic`}</span>
						<span className="whitespace-nowrap tabular-nums font-medium">
							{trafficBytes(item.used_bytes, unit)}
							<span className="text-muted-foreground">
								{" / "}
								{item.quota_bytes > 0 ? trafficBytes(item.quota_bytes, unit) : t`No quota`}
							</span>
						</span>
					</div>
					<div className="my-2 grid grid-cols-2 gap-3 rounded-md bg-muted/60 px-2.5 py-1.5">
						<div>
							<div className="text-muted-foreground">{t`Received`} (Rx)</div>
							<div className="whitespace-nowrap tabular-nums font-medium">{trafficBytes(item.rx_bytes, unit)}</div>
						</div>
						<div>
							<div className="text-muted-foreground">{t`Sent`} (Tx)</div>
							<div className="whitespace-nowrap tabular-nums font-medium">{trafficBytes(item.tx_bytes, unit)}</div>
						</div>
					</div>
					<div className="space-y-1.5">
						<div>
							<div className="text-muted-foreground">{t`Billing period`}</div>
							<div className="whitespace-nowrap tabular-nums" dir="ltr">
								{item.period_start} → {item.period_end_exclusive}
							</div>
							<div className="text-muted-foreground">{t`(end exclusive, UTC)`}</div>
						</div>
						<div className="text-muted-foreground">{countMode}</div>
						<div className="flex flex-wrap justify-between gap-x-2">
							<span className="text-muted-foreground">{t`Last sync`}</span>
							<span className="whitespace-nowrap tabular-nums" dir="ltr">
								{trafficTimestamp(item.last_success_at)}
							</span>
						</div>
					</div>
					{warning && <div className="mt-2 border-t pt-2 text-amber-700 dark:text-amber-300">{label}</div>}
					{item.error_code && <div className="break-all font-mono text-muted-foreground">{item.error_code}</div>}
				</TooltipContent>
			</Tooltip>
		)
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
	href,
	description,
	percent,
	countdown,
	...triggerProps
}: React.ComponentPropsWithRef<"a"> & {
	description: string
	percent?: number | null
	countdown?: string
}) {
	const width = trafficProgress(percent)
	const value = width == null ? "—" : `${decimalString(percent!, percent! >= 10 ? 1 : 2)}%`
	const meter = (
		<div
			className="flex gap-2 items-center tabular-nums tracking-tight w-full min-w-0"
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
	return href ? (
		<Link {...triggerProps} href={href} className="relative z-10 block w-full">
			{meter}
		</Link>
	) : (
		meter
	)
}

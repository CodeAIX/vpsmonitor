import { useState } from "react"
import { Trans, useLingui } from "@lingui/react/macro"
import { RefreshCwIcon } from "lucide-react"
import { Bar, BarChart, CartesianGrid, Legend, Tooltip, XAxis, YAxis } from "recharts"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { ChartContainer } from "@/components/ui/chart"
import { TrafficSettingsDialog } from "@/components/traffic/traffic-settings-dialog"
import { TrafficUsageCell, trafficStatusLabel } from "@/components/traffic/traffic-usage-cell"
import { useTrafficDetail } from "@/components/traffic/use-traffic"
import { trafficBytes, trafficTimestamp } from "@/lib/traffic-display"
import type { TrafficDay, TrafficSummary } from "@/types"

export default function TrafficPanel({ systemId }: { systemId: string }) {
	const { t } = useLingui()
	const [range, setRange] = useState(30)
	const { data, error, loading, reload } = useTrafficDetail(systemId, range)
	if (!data)
		return (
			<Card>
				<CardHeader>
					<CardTitle>
						<Trans>Traffic</Trans>
					</CardTitle>
				</CardHeader>
				<CardContent>
					{error ? (
						<div role="alert" className="flex flex-wrap items-center gap-3">
							<Trans>Traffic unavailable</Trans>
							<Button variant="outline" size="sm" onClick={reload}>
								<Trans>Retry</Trans>
							</Button>
						</div>
					) : (
						<div className="h-32 animate-pulse rounded bg-muted" aria-label={t`Loading traffic`} />
					)}
				</CardContent>
			</Card>
		)
	const unit = data.settings.display_unit
	function summaryCard(label: string, summary: TrafficSummary, billing = false) {
		return (
			<div className="min-w-0 rounded-lg border p-4">
				<div className="text-sm text-muted-foreground">{label}</div>
				<div
					className="mt-1 text-xl font-semibold tabular-nums"
					title={`${summary.rx_bytes ?? "—"} B Rx · ${summary.tx_bytes ?? "—"} B Tx`}
				>
					{trafficBytes(billing ? summary.used_bytes : summary.total_bytes, unit)}
				</div>
				<div className="mt-1 text-xs text-muted-foreground">
					Rx {trafficBytes(summary.rx_bytes, unit)} · Tx {trafficBytes(summary.tx_bytes, unit)}
				</div>
			</div>
		)
	}
	return (
		<Card className="min-w-0" id="traffic-panel">
			<CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
				<div className="grid gap-2">
					<CardTitle>
						<Trans>Traffic</Trans>
					</CardTitle>
					<CardDescription>
						<Trans>Daily vnStat accounting · UTC</Trans>
					</CardDescription>
				</div>
				<div className="flex flex-wrap gap-2">
					<Button variant="outline" size="sm" onClick={reload} disabled={loading} aria-label={t`Refresh traffic`}>
						<RefreshCwIcon className={`size-4 ${loading ? "animate-spin" : ""}`} />
					</Button>
					<TrafficSettingsDialog data={data} onSaved={reload} />
				</div>
			</CardHeader>
			<CardContent className="grid min-w-0 gap-5">
				{error && (
					<p role="alert" className="text-sm text-amber-600">
						<Trans>Unable to refresh. Showing last known data.</Trans>
					</p>
				)}
				<div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
					<span>{trafficStatusLabel(data.status)}</span>
					<span>{data.iface || t`No interface selected`}</span>
					<span>
						<Trans>Last sync</Trans>: {trafficTimestamp(data.last_success_at)}
					</span>
					{data.error_code && <span className="text-amber-600">{data.error_code}</span>}
				</div>
				<div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
					{summaryCard(t`Today`, data.today)}
					{summaryCard(t`Calendar month`, data.calendar_month)}
					{summaryCard(t`Billing period`, data.billing_cycle, true)}
					<div className="min-w-0 rounded-lg border p-4">
						<div className="text-sm text-muted-foreground">
							<Trans>Quota usage</Trans>
						</div>
						<div className="mt-1 text-xl font-semibold tabular-nums">
							{data.quota_bytes === 0 ? t`No quota` : data.percent == null ? "—" : `${data.percent.toFixed(1)}%`}
						</div>
						<div className="mt-1 text-xs text-muted-foreground">
							{data.quota_bytes > 0 ? trafficBytes(data.quota_bytes, unit) : "—"}
						</div>
					</div>
				</div>
				<div className="flex flex-wrap items-start justify-between gap-3 rounded-lg bg-muted/40 p-3">
					<div className="grid gap-1 text-xs text-muted-foreground">
						<span>
							{data.period_start} → {data.period_end_exclusive} <Trans>(end exclusive, UTC)</Trans>
						</span>
						<span>
							{data.count_mode === "tx" ? t`Outbound only` : t`Received + sent`} ·{" "}
							<Trans>Values are as of the last sync; today is still in progress.</Trans>
						</span>
						<span>
							<Trans>Interface counters may differ from the provider's bill.</Trans>
						</span>
					</div>
					<TrafficUsageCell item={data} error={error} />
				</div>
				<div className="flex flex-wrap items-center justify-between gap-2">
					<h4 className="text-sm font-medium">
						<Trans>Daily traffic (UTC)</Trans>
					</h4>
					<div className="flex gap-1">
						{[30, 90].map((days) => (
							<Button
								key={days}
								variant={range === days ? "secondary" : "ghost"}
								size="sm"
								onClick={() => setRange(days)}
							>
								{days} <Trans>days</Trans>
							</Button>
						))}
					</div>
				</div>
				{data.days?.some((day) => day.rx_bytes != null && day.tx_bytes != null) ? (
					<>
						<ChartContainer className="h-64 w-full min-w-0">
							<BarChart data={data.days} accessibilityLayer>
								<CartesianGrid vertical={false} />
								<XAxis dataKey="day" tickFormatter={(value) => String(value).slice(5)} minTickGap={24} />
								<YAxis width={72} tickFormatter={(value: number) => trafficBytes(value, unit)} />
								<Legend />
								<Tooltip
									content={({ active, payload }) => {
										if (!active || !payload?.length) return null
										const value = payload[0].payload as TrafficDay
										return (
											<div className="rounded border bg-popover p-3 text-xs shadow">
												<div>{value.day} UTC</div>
												<div>
													Rx: {trafficBytes(value.rx_bytes, unit)} ({value.rx_bytes?.toLocaleString()} B)
												</div>
												<div>
													Tx: {trafficBytes(value.tx_bytes, unit)} ({value.tx_bytes?.toLocaleString()} B)
												</div>
												{value.quality !== "normal" && <div className="text-amber-600">{value.quality}</div>}
											</div>
										)
									}}
								/>
								<Bar
									dataKey="rx_bytes"
									name={t`Received`}
									fill="var(--chart-2)"
									stackId="traffic"
									isAnimationActive={false}
								/>
								<Bar
									dataKey="tx_bytes"
									name={t`Sent`}
									fill="var(--chart-1)"
									stackId="traffic"
									isAnimationActive={false}
								/>
							</BarChart>
						</ChartContainer>
						<p className="text-xs text-muted-foreground">
							<Trans>Missing days are gaps, not zero usage. Historical coverage has not been fully verified.</Trans>
						</p>
					</>
				) : (
					<div className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">
						<p>
							<Trans>No daily traffic collected yet.</Trans>
						</p>
						<a
							className="mt-2 inline-block underline"
							href="https://github.com/CodeAIX/vpsmonitor/blob/plus/main/docs/traffic/DOCKER_AGENT.md"
							target="_blank"
							rel="noreferrer"
						>
							<Trans>Set up Docker traffic collection</Trans>
						</a>
					</div>
				)}
			</CardContent>
		</Card>
	)
}

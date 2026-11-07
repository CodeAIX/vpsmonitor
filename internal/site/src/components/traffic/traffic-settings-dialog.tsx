import { useState, type FormEvent } from "react"
import { Trans, useLingui } from "@lingui/react/macro"
import { Settings2Icon } from "lucide-react"
import type { TrafficView } from "@/types"
import { isReadOnlyUser } from "@/lib/api"
import { trafficAPI } from "@/lib/traffic-api"
import { trafficQuota, trafficQuotaInput } from "@/lib/traffic-display"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
	DialogTrigger,
} from "@/components/ui/dialog"

const selectClass = "h-10 w-full rounded-md border bg-background px-3 text-sm"

export function TrafficSettingsDialog({ data, onSaved }: { data: TrafficView; onSaved: () => void }) {
	const { t } = useLingui()
	const [open, setOpen] = useState(false)
	const [iface, setIface] = useState(data.settings.iface)
	const [quota, setQuota] = useState("0")
	const [factor, setFactor] = useState(1e9)
	const [mode, setMode] = useState(data.settings.count_mode)
	const [day, setDay] = useState(data.settings.billing_day)
	const [unit, setUnit] = useState(data.settings.display_unit)
	const [enabled, setEnabled] = useState(data.settings.enabled)
	const [error, setError] = useState(false)
	const [saving, setSaving] = useState(false)
	const readonly = isReadOnlyUser()
	function changeOpen(value: boolean) {
		if (value) {
			const settings = data.settings,
				divisor = settings.quota_bytes >= 1e12 ? 1e12 : 1e9
			setIface(settings.iface)
			setFactor(divisor)
			setQuota(trafficQuotaInput(settings.quota_bytes, divisor))
			setMode(settings.count_mode)
			setDay(settings.billing_day)
			setUnit(settings.display_unit)
			setEnabled(settings.enabled)
			setError(false)
		}
		setOpen(value)
	}
	async function save(event: FormEvent) {
		event.preventDefault()
		if (readonly || saving) return
		setSaving(true)
		setError(false)
		try {
			await trafficAPI.settings(data.system, {
				...(iface ? { iface } : {}),
				quota_bytes: trafficQuota(quota, factor),
				count_mode: mode,
				billing_day: day,
				billing_timezone: "UTC",
				display_unit: unit,
				enabled,
			})
			setOpen(false)
			onSaved()
		} catch {
			setError(true)
		} finally {
			setSaving(false)
		}
	}
	return (
		<Dialog open={open} onOpenChange={changeOpen}>
			<DialogTrigger asChild>
				<Button variant="outline" size="sm">
					<Settings2Icon className="mr-1.5 size-4" />
					<Trans>Traffic settings</Trans>
				</Button>
			</DialogTrigger>
			<DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
				<DialogHeader>
					<DialogTitle>
						<Trans>Traffic settings</Trans>
					</DialogTitle>
					<DialogDescription>
						<Trans>Choose one billing interface. All daily boundaries use UTC.</Trans>
					</DialogDescription>
				</DialogHeader>
				<form onSubmit={save} className="grid gap-4">
					<div className="grid gap-2">
						<Label htmlFor="traffic-iface">
							<Trans>Billing interface</Trans>
						</Label>
						<select
							id="traffic-iface"
							className={selectClass}
							value={iface}
							onChange={(event) => setIface(event.target.value)}
							disabled={readonly || saving}
						>
							<option value="">{t`Choose an interface`}</option>
							{data.sources.map((source) => (
								<option key={source.iface} value={source.iface}>
									{source.iface}
								</option>
							))}
						</select>
					</div>
					{/^(tailscale|docker|br-)/.test(iface) && (
						<p role="status" className="text-sm text-amber-600 dark:text-amber-400">
							<Trans>This virtual interface may count traffic already recorded by the public interface.</Trans>
						</p>
					)}
					{iface !== data.settings.iface && data.settings.iface && (
						<p className="text-sm text-amber-600">
							<Trans>
								Changing interfaces can make this billing period discontinuous. Interfaces are never added together.
							</Trans>
						</p>
					)}
					<div className="grid grid-cols-[1fr_6rem] gap-2">
						<div className="grid gap-2">
							<Label htmlFor="traffic-quota">
								<Trans>Quota</Trans>
							</Label>
							<Input
								id="traffic-quota"
								type="text"
								inputMode="decimal"
								value={quota}
								onChange={(event) => setQuota(event.target.value)}
								disabled={readonly || saving}
							/>
						</div>
						<div className="grid gap-2">
							<Label htmlFor="traffic-quota-unit">
								<Trans>Unit</Trans>
							</Label>
							<select
								id="traffic-quota-unit"
								className={selectClass}
								value={factor}
								disabled={readonly || saving}
								onChange={(event) => {
									const next = Number(event.target.value)
									try {
										setQuota(trafficQuotaInput(trafficQuota(quota, factor), next))
									} catch {}
									setFactor(next)
								}}
							>
								<option value={1e9}>GB</option>
								<option value={1e12}>TB</option>
							</select>
						</div>
					</div>
					<p className="-mt-2 text-xs text-muted-foreground">
						<Trans>0 means no quota. 1 TB = 1,000 GB.</Trans>
					</p>
					<div className="grid gap-4 sm:grid-cols-2">
						<div className="grid gap-2">
							<Label htmlFor="traffic-mode">
								<Trans>Count mode</Trans>
							</Label>
							<select
								id="traffic-mode"
								className={selectClass}
								value={mode}
								onChange={(event) => setMode(event.target.value as typeof mode)}
								disabled={readonly || saving}
							>
								<option value="tx">{t`Outbound only`}</option>
								<option value="rx_tx">{t`Received + sent`}</option>
							</select>
						</div>
						<div className="grid gap-2">
							<Label htmlFor="traffic-billing-day">
								<Trans>Billing day (UTC)</Trans>
							</Label>
							<select
								id="traffic-billing-day"
								className={selectClass}
								value={day}
								onChange={(event) => setDay(Number(event.target.value))}
								disabled={readonly || saving}
							>
								{Array.from({ length: 28 }, (_, index) => (
									<option key={index + 1} value={index + 1}>
										{index + 1}
									</option>
								))}
							</select>
						</div>
					</div>
					<div className="grid gap-2">
						<Label htmlFor="traffic-display-unit">
							<Trans>Display units</Trans>
						</Label>
						<select
							id="traffic-display-unit"
							className={selectClass}
							value={unit}
							onChange={(event) => setUnit(event.target.value as typeof unit)}
							disabled={readonly || saving}
						>
							<option value="GB">GB / TB (1000)</option>
							<option value="GiB">GiB / TiB (1024)</option>
						</select>
					</div>
					<label className="flex items-center gap-2 text-sm">
						<input
							type="checkbox"
							checked={enabled}
							onChange={(event) => setEnabled(event.target.checked)}
							disabled={readonly || saving}
						/>
						<Trans>Show traffic usage</Trans>
					</label>
					{error && (
						<p role="alert" className="text-sm text-destructive">
							<Trans>Unable to save. Check the quota, interface and permissions.</Trans>
						</p>
					)}
					{readonly ? (
						<p className="text-sm text-muted-foreground">
							<Trans>This account can view settings but cannot change them.</Trans>
						</p>
					) : (
						<Button type="submit" disabled={saving || (!iface && data.sources.length > 0)}>
							{saving ? t`Saving…` : t`Save settings`}
						</Button>
					)}
				</form>
			</DialogContent>
		</Dialog>
	)
}

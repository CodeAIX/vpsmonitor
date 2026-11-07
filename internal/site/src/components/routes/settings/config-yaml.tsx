import { t } from "@lingui/core/macro"
import { Trans } from "@lingui/react/macro"
import { redirectPage } from "@nanostores/router"
import clsx from "clsx"
import { FileSlidersIcon, LoaderCircleIcon } from "lucide-react"
import { useState } from "react"
import { $router } from "@/components/router"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"
import { Textarea } from "@/components/ui/textarea"
import { toast } from "@/components/ui/use-toast"
import { isAdmin, pb } from "@/lib/api"
import ImportSystems from "./import-systems"

type SyncPreview = {
	rows: { id?: string; name: string; action: "add" | "update" | "keep"; ownership_changed: boolean }[]
	retained: number
}

export default function ConfigYaml() {
	const [configContent, setConfigContent] = useState<string>("")
	const [isLoading, setIsLoading] = useState(false)
	const [includeTraffic, setIncludeTraffic] = useState(true)
	const [exportFormat, setExportFormat] = useState("combined")

	const [syncPreview, setSyncPreview] = useState<SyncPreview>()
	const [previewLoading, setPreviewLoading] = useState(false)

	async function previewSync(file: File) {
		setSyncPreview(undefined)
		setPreviewLoading(true)
		try {
			if (file.size > 1024 * 1024) throw new Error(t`The YAML file must be 1 MiB or smaller.`)
			setSyncPreview(
				await pb.send<SyncPreview>("/api/vpsmonitor/config-yaml/sync-preview", {
					method: "POST",
					body: { config: await file.text() },
				})
			)
		} catch (error: any) {
			toast({ title: t`Error`, description: error.message, variant: "destructive" })
		} finally {
			setPreviewLoading(false)
		}
	}

	const ButtonIcon = isLoading ? LoaderCircleIcon : FileSlidersIcon

	async function fetchConfig(format: string) {
		try {
			setIsLoading(true)
			const { config } = await pb.send<{ config: string }>("/api/vpsmonitor/config-yaml", { query: { format } })
			setConfigContent(config)
			setExportFormat(format)
		} catch (error: any) {
			toast({
				title: t`Error`,
				description: error.message,
				variant: "destructive",
			})
		} finally {
			setIsLoading(false)
		}
	}

	if (!isAdmin()) {
		redirectPage($router, "settings", { name: "general" })
	}

	return (
		<div>
			<ImportSystems />
			<Separator className="my-6" />
			<div>
				<h3 className="text-xl font-medium mb-2">
					<Trans>Export configuration</Trans>
				</h3>
				<p className="text-sm text-muted-foreground leading-relaxed">
					<Trans>Export your current systems configuration.</Trans>
				</p>
			</div>
			<Separator className="my-4" />
			<div className="space-y-2">
				<details className="mb-4">
					<summary className="text-sm cursor-pointer py-1">
						<Trans>Manage systems with config.yml (advanced)</Trans>
					</summary>
					<p className="text-sm text-muted-foreground leading-relaxed my-1">
						<Trans>
							Systems may be managed in a <code className="bg-muted rounded-sm px-1 text-primary">config.yml</code> file
							inside your data directory.
						</Trans>
					</p>
					<p className="text-sm text-muted-foreground leading-relaxed">
						<Trans>
							Startup sync matches by device ID first and keeps devices outside the file. Omit users to keep ownership.
						</Trans>
					</p>
					<div className="my-3 flex flex-wrap items-center gap-3">
						<Button type="button" variant="outline" onClick={() => fetchConfig("sync")} disabled={isLoading}>
							<Trans>Export for this Hub</Trans>
						</Button>
						<Label htmlFor="sync-preview-file">
							<Trans>Preview startup sync</Trans>
						</Label>
						<input
							id="sync-preview-file"
							type="file"
							accept=".yaml,.yml"
							disabled={previewLoading}
							className="text-sm file:me-2 file:rounded-md file:bg-muted file:px-2 file:py-1"
							onChange={(e) => {
								const file = e.currentTarget.files?.[0]
								e.currentTarget.value = ""
								if (file) void previewSync(file)
							}}
						/>
					</div>
					{syncPreview && (
						<div className="my-3 text-sm" aria-live="polite">
							<p className="text-muted-foreground">
								<Trans>Unlisted devices kept: {syncPreview.retained}. Preview only; no changes applied.</Trans>
							</p>
							<ul className="mt-2 max-h-48 overflow-auto space-y-1">
								{syncPreview.rows.map((row, index) => (
									<li key={row.id ?? index} className="break-words">
										{row.name} · {row.action === "add" ? t`Add` : row.action === "update" ? t`Update` : t`Keep`}
										{row.ownership_changed && (
											<>
												{" "}
												· <Trans>Ownership changes</Trans>
											</>
										)}
									</li>
								))}
							</ul>
						</div>
					)}
				</details>
				{configContent && (
					<Textarea
						dir="ltr"
						autoFocus
						value={configContent}
						readOnly
						spellCheck="false"
						rows={Math.min(25, configContent.split("\n").length)}
						className="font-mono whitespace-pre"
					/>
				)}
			</div>
			<Separator className="my-5" />
			<div className="flex items-center gap-2 my-4">
				<Checkbox
					id="export-include-traffic"
					checked={includeTraffic}
					onCheckedChange={(v) => setIncludeTraffic(v === true)}
					disabled={isLoading}
				/>
				<Label htmlFor="export-include-traffic">
					<Trans>Include traffic settings in system configuration</Trans>
				</Label>
			</div>
			<div className="flex flex-wrap gap-3">
				<Button
					type="button"
					className="flex items-center gap-1"
					onClick={() => fetchConfig(includeTraffic ? "combined" : "classic")}
					disabled={isLoading}
				>
					<ButtonIcon className={clsx("h-4 w-4 me-0.5", isLoading && "animate-spin")} />
					<Trans>Export configuration</Trans>
				</Button>
				<Button type="button" variant="outline" onClick={() => fetchConfig("traffic")} disabled={isLoading}>
					<Trans>Export traffic settings only</Trans>
				</Button>
				{configContent && (
					<Button
						type="button"
						variant="outline"
						onClick={() => {
							const url = URL.createObjectURL(new Blob([configContent], { type: "text/yaml;charset=utf-8" }))
							const link = document.createElement("a")
							link.href = url
							link.download =
								exportFormat === "sync"
									? "config.yml"
									: exportFormat === "traffic"
										? "vpsmonitor-traffic.yml"
										: "vpsmonitor-config.yml"
							link.click()
							setTimeout(() => URL.revokeObjectURL(url), 1000)
						}}
					>
						<Trans>Download YAML</Trans>
					</Button>
				)}
			</div>
		</div>
	)
}

import { t } from "@lingui/core/macro"
import { Trans } from "@lingui/react/macro"
import { redirectPage } from "@nanostores/router"
import clsx from "clsx"
import { AlertCircleIcon, FileSlidersIcon, LoaderCircleIcon } from "lucide-react"
import { useState } from "react"
import { $router } from "@/components/router"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"
import { Textarea } from "@/components/ui/textarea"
import { toast } from "@/components/ui/use-toast"
import { isAdmin, pb } from "@/lib/api"
import ImportSystems from "./import-systems"

export default function ConfigYaml() {
	const [configContent, setConfigContent] = useState<string>("")
	const [isLoading, setIsLoading] = useState(false)
	const [includeTraffic, setIncludeTraffic] = useState(true)
	const [exportFormat, setExportFormat] = useState("combined")

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
							On each restart, systems in the database will be updated to match the systems defined in the file.
						</Trans>
					</p>
					<Alert className="my-4 border-destructive text-destructive w-auto table md:pe-6">
						<AlertCircleIcon className="size-4.5 stroke-destructive" />
						<AlertTitle>
							<Trans>Caution - potential data loss</Trans>
						</AlertTitle>
						<AlertDescription>
							<p>
								<Trans>
									Existing systems not defined in <code>config.yml</code> will be deleted. Please make regular backups.
								</Trans>
							</p>
						</AlertDescription>
					</Alert>
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
							link.download = exportFormat === "traffic" ? "vpsmonitor-traffic.yml" : "vpsmonitor-config.yml"
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

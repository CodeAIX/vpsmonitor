import { memo } from "react"
import { t } from "@lingui/core/macro"
import { copyToClipboard, getAgentHubURL } from "@/lib/utils"
import { agentCompose } from "@/lib/deployment"
import { DropdownMenuContent, DropdownMenuItem } from "./ui/dropdown-menu"
import { toast } from "./ui/use-toast"

export function copyDockerCompose(key: string, token: string) {
	const app = globalThis.VPSMONITOR
	const hub = getAgentHubURL()
	if (!hub || !key || !token) {
		toast({ description: t`Agent installation settings are not ready`, variant: "destructive" })
		return
	}
	copyToClipboard(agentCompose(key, token, hub, app.PRODUCT_COMMIT, app.PRODUCT_VERSION, app.AGENT_IMAGE_TAG))
}

export interface DropdownItem {
	text: string
	onClick?: () => void
	url?: string
	icons?: React.ComponentType<React.SVGProps<SVGSVGElement>>[]
}

export const InstallDropdown = memo(({ items }: { items: DropdownItem[] }) => {
	return (
		<DropdownMenuContent align="end">
			{items.map((item, index) => {
				const className = "cursor-pointer flex items-center gap-1.5"
				return item.url ? (
					<DropdownMenuItem key={index} asChild>
						<a href={item.url} className={className} target="_blank" rel="noopener noreferrer">
							{item.text}{" "}
							{item.icons?.map((Icon, iconIndex) => (
								<Icon key={iconIndex} className="size-4" />
							))}
						</a>
					</DropdownMenuItem>
				) : (
					<DropdownMenuItem key={index} onClick={item.onClick} className={className}>
						{item.text}{" "}
						{item.icons?.map((Icon, iconIndex) => (
							<Icon key={iconIndex} className="size-4" />
						))}
					</DropdownMenuItem>
				)
			})}
		</DropdownMenuContent>
	)
})

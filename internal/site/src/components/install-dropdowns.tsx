import { memo } from "react"
import { copyToClipboard, getHubURL } from "@/lib/utils"
import { agentCompose, agentRun } from "@/lib/deployment"
import { DropdownMenuContent, DropdownMenuItem } from "./ui/dropdown-menu"

export function copyDockerCompose(_port = "45876", key: string, token: string) {
 copyToClipboard(agentCompose(key, token, getHubURL(), globalThis.VPSMONITOR.PRODUCT_COMMIT, globalThis.VPSMONITOR.PRODUCT_VERSION))
}
export function copyDockerRun(_port = "45876", key: string, token: string) {
 copyToClipboard(agentRun(key, token, getHubURL(), globalThis.VPSMONITOR.PRODUCT_COMMIT, globalThis.VPSMONITOR.PRODUCT_VERSION))
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

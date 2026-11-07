export function Logo({ className }: { className?: string }) {
	return (
		<svg role="img" aria-label="vpsmonitor" viewBox="0 0 222 40" className={className}>
			<rect x="1" y="4" width="32" height="32" rx="8" className="fill-chart-2/20" />
			<path d="m8 15 8 13 11-17" fill="none" className="stroke-chart-2" strokeWidth="3.5" strokeLinecap="round" strokeLinejoin="round" />
			<text x="42" y="30" fill="currentColor" fontFamily="ui-sans-serif,system-ui,sans-serif" fontWeight="780" fontSize="31" letterSpacing="-1.1">vpsmonitor</text>
		</svg>
	)
}

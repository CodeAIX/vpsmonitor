import { pb } from "./api"
import type { TrafficOverview, TrafficSettings, TrafficView } from "@/types"

type Options = { method?: string; query?: Record<string, string | number>; body?: unknown; requestKey: null }
type Transport = <T>(path: string, options: Options) => Promise<T>

/** Short-lived, auth-scoped cache. No traffic or credentials enter localStorage. */
export function createTrafficAPI(send: Transport, auth: () => string, now = Date.now) {
	const cache = new Map<string, { promise: Promise<unknown>; expires: number; pending: boolean }>()
	let session = ""
	function invalidate() {
		cache.clear()
	}
	function read<T>(
		key: string,
		path: string,
		query?: Record<string, string | number>,
		load?: () => Promise<T>
	): Promise<T> {
		const token = auth()
		if (token !== session) {
			invalidate()
			session = token
		}
		const previous = cache.get(key)
		if (previous && (previous.pending || previous.expires > now())) return previous.promise as Promise<T>
		const entry = { promise: Promise.resolve(undefined) as Promise<unknown>, expires: now() + 60_000, pending: true }
		entry.promise = (load ? load() : send<T>(path, { query, requestKey: null }))
			.then((result) => {
				if (auth() !== token || cache.get(key) !== entry) throw new Error("Traffic request superseded")
				entry.pending = false
				return result
			})
			.catch((error) => {
				if (cache.get(key) === entry) cache.delete(key)
				throw error
			})
		if (cache.size >= 100) cache.delete(cache.keys().next().value!)
		cache.set(key, entry)
		return entry.promise as Promise<T>
	}
	return {
		invalidate,
		overview: () =>
			read<TrafficOverview>("overview", "/api/vpsmonitor/traffic/overview", undefined, async () => {
				const items: TrafficView[] = []
				let page: TrafficOverview
				do {
					page = await send<TrafficOverview>("/api/vpsmonitor/traffic/overview", {
						query: { limit: 50, offset: items.length },
						requestKey: null,
					})
					items.push(...page.items)
				} while (page.has_more && page.items.length && items.length < 500)
				return { ...page, items }
			}),
		detail: (system: string, from: string, to: string) =>
			read<TrafficView>(`detail:${system}:${from}:${to}`, "/api/vpsmonitor/traffic/detail", { system, from, to }),
		async settings(system: string, settings: Partial<Omit<TrafficSettings, "system">>) {
			const token = auth()
			const result = await send<TrafficView>("/api/vpsmonitor/traffic/settings", {
				method: "PATCH",
				query: { system },
				body: settings,
				requestKey: null,
			})
			invalidate()
			if (auth() !== token) throw new Error("Traffic session changed")
			return result
		},
	}
}

export const trafficAPI = createTrafficAPI(
	(path, options) => pb.send(path, options),
	() => pb.authStore.token
)

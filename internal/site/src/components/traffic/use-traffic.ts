import { useCallback, useEffect, useRef, useState } from "react"
import { trafficAPI } from "@/lib/traffic-api"
import { trafficRange } from "@/lib/traffic-display"

function useTrafficResource<T>(load: () => Promise<T>) {
	const [data, setData] = useState<T>()
	const [error, setError] = useState(false)
	const [loading, setLoading] = useState(true)
	const generation = useRef(0)
	const request = useCallback(() => {
		const version = ++generation.current
		setLoading(true)
		load()
			.then((value) => {
				if (generation.current === version) {
					setData(value)
					setError(false)
				}
			})
			.catch(() => {
				if (generation.current === version) setError(true)
			})
			.finally(() => {
				if (generation.current === version) setLoading(false)
			})
	}, [load])
	useEffect(() => {
		setData(undefined)
		request()
		const timer = setInterval(request, 60_000)
		return () => {
			++generation.current
			clearInterval(timer)
		}
	}, [request])
	const reload = useCallback(() => {
		trafficAPI.invalidate()
		request()
	}, [request])
	return { data, error, loading, reload }
}

export function useTrafficOverview() {
	return useTrafficResource(useCallback(() => trafficAPI.overview(), []))
}

export function useTrafficDetail(system: string, days: number) {
	return useTrafficResource(
		useCallback(() => {
			const range = trafficRange(days)
			return trafficAPI.detail(system, range.from, range.to)
		}, [system, days])
	)
}

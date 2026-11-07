import { expect, test } from "bun:test"
import { mergeHistory } from "../src/lib/history-merge"
import type { HistoryPoint } from "../src/lib/history-api"

const width = 120 * 60_000
const bucket = Math.floor(1_790_000_000_000 / width) * width
function point(complete: boolean, at = bucket): HistoryPoint {
	return {
		bucket: at,
		interval: width,
		created: at + width - 60_000,
		samples: complete ? 120 : 3,
		complete,
		stats: { cpu: 42 },
		containers: [{ n: "sample", c: 2 }],
	}
}

test("late historical buckets fill a week chart without removing unrelated live points", () => {
	const live = [{ created: bucket + width + 60_000, stats: { cpu: 5 } }]
	const result = mergeHistory(live, [point(true)])
	expect(result).toHaveLength(2)
	expect(result[0].stats.cpu).toBe(42)
	expect(result[1]).toBe(live[0])
})

test("a complete recovered bucket replaces overlapping normal points once, including on repeated refresh", () => {
	const live = [
		{ created: bucket + 60_000, stats: { cpu: 9 } },
		{ created: bucket + 120_000, stats: { cpu: 12 } },
	]
	const first = mergeHistory(live, [point(true)])
	const second = mergeHistory(first, [point(true)])
	expect(first).toHaveLength(1)
	expect(second).toEqual(first)
})

test("partial recovery fills gaps but never displaces existing data or fabricates zero values", () => {
	const live = [{ created: bucket + 60_000, stats: { cpu: 9 } }]
	expect(mergeHistory(live, [point(false)])).toBe(live)
	const recovered = mergeHistory([], [point(false)])
	expect(recovered).toHaveLength(1)
	expect(recovered[0]).toEqual({ created: bucket + width - 60_000, stats: { cpu: 42 } })
	expect(mergeHistory(live, [])).toBe(live)
})

test("Docker replay uses container snapshots; an empty history snapshot cannot erase live Docker stats", () => {
	const live = [{ created: bucket + 60_000, stats: [{ n: "sample", c: 1 }] }]
	expect(mergeHistory(live, [point(true)], true)[0].stats).toEqual([{ n: "sample", c: 2 }])
	expect(mergeHistory(live, [{ ...point(true), containers: [] }], true)).toBe(live)
})

test("minute charts preserve a live sample and only fill missing minutes", () => {
	const live = [{ created: bucket + 30_000, stats: { cpu: 9 } }]
	const cached = { ...point(true), bucket, interval: 60_000, created: bucket + 15_000, samples: 1 }
	expect(mergeHistory(live, [cached])).toBe(live)
	expect(mergeHistory([], [cached])).toEqual([{ created: bucket + 15_000, stats: { cpu: 42 } }])
})

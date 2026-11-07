// Browser storage used by stores at module initialization. Test-only preload;
// production continues to use the browser's own Storage implementation.
const values = new Map<string, string>()
Object.defineProperty(globalThis, "localStorage", {
	value: {
		getItem: (key: string) => values.get(key) ?? null,
		setItem: (key: string, value: string) => values.set(key, String(value)),
		removeItem: (key: string) => values.delete(key),
		clear: () => values.clear(),
		key: (index: number) => Array.from(values.keys())[index] ?? null,
		get length() { return values.size },
	},
	configurable: true,
})

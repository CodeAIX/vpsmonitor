import agentTemplate from "../../../../deploy/release/agent.compose.yml?raw"

export function sourceRef(value: string): string {
	return /^[a-f0-9]{40}$/.test(value) ? value : "main"
}

const composeQuote = (value: string) => JSON.stringify(value.replaceAll("$", () => "$$"))

// The release download and the Hub both use the same template. The Hub fills
// in credentials and pins its version. Source builds supply a build context.
export function agentCompose(key: string, token: string, hub: string, commit: string, version = "1.0.0-dev", tag = ""): string {
	const release = /^v\d+\.\d+\.\d+(?:-(?:alpha|beta|rc)\.\d+)?$/.test(tag)
	const image = release ? `    image: ghcr.io/codeaix/vpsmonitor-agent:${tag}` : `    image: vpsmonitor-agent:dev
    build:
      context: https://github.com/CodeAIX/vpsmonitor.git#${sourceRef(commit)}
      dockerfile: Dockerfile.agent
      args:
        COMMIT: ${sourceRef(commit)}
        VERSION: ${composeQuote(version)}`
	const credentials = { KEY: key, TOKEN: token, HUB_URL: hub }
	return agentTemplate
		.replace(/^    image: .*$/m, () => image)
		.replace(/^      (KEY|TOKEN|HUB_URL): .*$/gm, (_, name: keyof typeof credentials) =>
			`      ${name}: ${composeQuote(credentials[name])}`,
		)
}

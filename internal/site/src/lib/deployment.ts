export function sourceRef(value: string): string {
	return /^[a-f0-9]{40}$/.test(value) ? value : "main"
}

function releaseAgentImage(tag: string): string | undefined {
	if (/^v\d+\.\d+\.\d+(?:-(?:alpha|beta|rc)\.\d+)?$/.test(tag)) {
		return `ghcr.io/codeaix/vpsmonitor-agent:${tag}`
	}
}

const composeQuote = (value: string) => JSON.stringify(value.replaceAll("$", () => "$$"))

// Only a Hub built by the release workflow supplies an image tag. Source
// builds still offer a Docker build tied to their source revision.
export function agentCompose(key: string, token: string, hub: string, commit: string, version = "0.1.0-dev", tag = ""): string {
	const image = releaseAgentImage(tag)
	const spec = image ? `image: ${image}` : `image: vpsmonitor-agent:dev
    build:
      context: https://github.com/CodeAIX/vpsmonitor.git#${sourceRef(commit)}
      dockerfile: Dockerfile.agent
      args:
        COMMIT: ${sourceRef(commit)}
        VERSION: ${composeQuote(version)}`
	return `services:
  vpsmonitor-agent:
    ${spec}
    container_name: vpsmonitor-agent
    restart: unless-stopped
    network_mode: host
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    volumes:
      - ./vpsmonitor_agent_data:/var/lib/vpsmonitor-agent
      - /var/run/docker.sock:/var/run/docker.sock:ro
    environment:
      KEY: ${composeQuote(key)}
      TOKEN: ${composeQuote(token)}
      HUB_URL: ${composeQuote(hub)}
      DISABLE_SSH: "true"
      VPSMONITOR_NETWORK_SCOPE: host
      VPSMONITOR_TRAFFIC_INTERFACE: auto
`
}

const shellQuote = (value: string) => `'${value.replaceAll("'", "'\"'\"'")}'`

export function agentRun(key: string, token: string, hub: string, commit: string, version = "0.1.0-dev", tag = ""): string {
	const image = releaseAgentImage(tag)
	const build = image ? "" : `docker build -t vpsmonitor-agent:dev --build-arg COMMIT=${sourceRef(commit)} --build-arg VERSION=${shellQuote(version)} -f Dockerfile.agent ${shellQuote(`https://github.com/CodeAIX/vpsmonitor.git#${sourceRef(commit)}`)} && `
	return `${build}docker run -d --name vpsmonitor-agent --network host --restart unless-stopped --cap-drop ALL --security-opt no-new-privileges -v vpsmonitor_agent_data:/var/lib/vpsmonitor-agent -v /var/run/docker.sock:/var/run/docker.sock:ro -e DISABLE_SSH=true -e VPSMONITOR_NETWORK_SCOPE=host -e VPSMONITOR_TRAFFIC_INTERFACE=auto -e KEY=${shellQuote(key)} -e TOKEN=${shellQuote(token)} -e HUB_URL=${shellQuote(hub)} ${image || "vpsmonitor-agent:dev"}`
}

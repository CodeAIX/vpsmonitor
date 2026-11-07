export function sourceRef(value: string): string {
	return /^[a-f0-9]{40}$/.test(value) ? value : "main"
}

// Development builds use the exact public source revision. No unpublished
// image tag or third-party installer domain is presented as an installation.
export function agentCompose(key: string, token: string, hub: string, commit: string, version = "0.1.0-dev"): string {
	return `services:
  vpsmonitor-agent:
    image: vpsmonitor-agent:dev
    build:
      context: https://github.com/CodeAIX/vpsmonitor.git#${sourceRef(commit)}
      dockerfile: Dockerfile.agent
      args:
        COMMIT: ${sourceRef(commit)}
        VERSION: ${JSON.stringify(version.replaceAll("$", () => "$$"))}
    container_name: vpsmonitor-agent
    restart: unless-stopped
    network_mode: host
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    volumes:
      - ./vpsmonitor_agent_data:/var/lib/vpsmonitor-agent
      - /var/run/docker.sock:/var/run/docker.sock:ro
    environment:
      KEY: ${JSON.stringify(key.replaceAll("$", () => "$$"))}
      TOKEN: ${JSON.stringify(token.replaceAll("$", () => "$$"))}
      HUB_URL: ${JSON.stringify(hub.replaceAll("$", () => "$$"))}
      DISABLE_SSH: "true"
      VPSMONITOR_NETWORK_SCOPE: host
      VPSMONITOR_TRAFFIC_INTERFACE: auto
`
}

const shellQuote = (value: string) => `'${value.replaceAll("'", "'\"'\"'")}'`

export function agentRun(key: string, token: string, hub: string, commit: string, version = "0.1.0-dev"): string {
	return `docker build -t vpsmonitor-agent:dev --build-arg COMMIT=${sourceRef(commit)} --build-arg VERSION=${shellQuote(version)} -f Dockerfile.agent ${shellQuote(`https://github.com/CodeAIX/vpsmonitor.git#${sourceRef(commit)}`)} && docker run -d --name vpsmonitor-agent --network host --restart unless-stopped --cap-drop ALL --security-opt no-new-privileges -v vpsmonitor_agent_data:/var/lib/vpsmonitor-agent -v /var/run/docker.sock:/var/run/docker.sock:ro -e DISABLE_SSH=true -e VPSMONITOR_NETWORK_SCOPE=host -e VPSMONITOR_TRAFFIC_INTERFACE=auto -e KEY=${shellQuote(key)} -e TOKEN=${shellQuote(token)} -e HUB_URL=${shellQuote(hub)} vpsmonitor-agent:dev`
}

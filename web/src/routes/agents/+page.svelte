<script lang="ts">
	import { agents as agentsApi } from '$lib/api/client';
	import { user } from '$lib/stores/auth';
	import AgentCard from '$lib/components/AgentCard.svelte';

	let agentList = $state<any[]>([]);
	let loadingData = $state(true);
	let showRegister = $state(false);

	let newName = $state('');
	let newDisplayName = $state('');
	let registering = $state(false);
	let registerError = $state('');
	let newApiKey = $state('');
	let copiedField = $state('');

	type ClientId = 'claude-code' | 'gemini' | 'cursor' | 'windsurf' | 'vscode' | 'claude-desktop';
	type AuthMode = 'apikey' | 'oauth';

	let selectedClient = $state<ClientId>('claude-code');
	let authMode = $state<AuthMode>('apikey');

	const clients: { id: ClientId; label: string; icon: string }[] = [
		{ id: 'claude-code', label: 'Claude Code', icon: 'C' },
		{ id: 'gemini', label: 'Gemini CLI', icon: 'G' },
		{ id: 'cursor', label: 'Cursor', icon: 'Cu' },
		{ id: 'windsurf', label: 'Windsurf', icon: 'W' },
		{ id: 'vscode', label: 'VS Code', icon: 'VS' },
		{ id: 'claude-desktop', label: 'Claude Desktop', icon: 'CD' },
	];

	let mcpUrl = $derived(typeof window !== 'undefined' ? `${window.location.origin}/mcp` : 'http://localhost:8080/mcp');

	function getCliCommand(client: ClientId, mode: AuthMode, apiKey: string): string | null {
		if (mode === 'apikey') {
			switch (client) {
				case 'claude-code':
					return `claude mcp add --transport http synapbus ${mcpUrl} \\
  --header "Authorization: Bearer ${apiKey}"`;
				case 'gemini':
					return `gemini mcp add --transport http \\
  --header "Authorization: Bearer ${apiKey}" \\
  synapbus ${mcpUrl}`;
				default:
					return null;
			}
		} else {
			switch (client) {
				case 'claude-code':
					return `claude mcp add --transport http synapbus ${mcpUrl}`;
				case 'gemini':
					return `gemini mcp add --transport http synapbus ${mcpUrl}`;
				default:
					return null;
			}
		}
	}

	function getConfigJson(client: ClientId, mode: AuthMode, apiKey: string): string {
		const url = mcpUrl;
		if (mode === 'oauth') {
			switch (client) {
				case 'claude-code':
					return JSON.stringify({ mcpServers: { synapbus: { type: "http", url } } }, null, 2);
				case 'gemini':
					return JSON.stringify({ mcpServers: { synapbus: { httpUrl: url } } }, null, 2);
				case 'cursor':
					return JSON.stringify({ mcpServers: { synapbus: { url } } }, null, 2);
				case 'windsurf':
					return JSON.stringify({ mcpServers: { synapbus: { serverUrl: url } } }, null, 2);
				case 'vscode':
					return JSON.stringify({ servers: { synapbus: { type: "http", url } } }, null, 2);
				case 'claude-desktop':
					return `Add via Settings > Connectors in Claude Desktop.\nURL: ${url}\nOAuth login will be handled automatically.`;
			}
		}
		switch (client) {
			case 'claude-code':
				return JSON.stringify({ mcpServers: { synapbus: { type: "http", url, headers: { Authorization: `Bearer ${apiKey}` } } } }, null, 2);
			case 'gemini':
				return JSON.stringify({ mcpServers: { synapbus: { httpUrl: url, headers: { Authorization: `Bearer ${apiKey}` } } } }, null, 2);
			case 'cursor':
				return JSON.stringify({ mcpServers: { synapbus: { url, headers: { Authorization: `Bearer ${apiKey}` } } } }, null, 2);
			case 'windsurf':
				return JSON.stringify({ mcpServers: { synapbus: { serverUrl: url, headers: { Authorization: `Bearer ${apiKey}` } } } }, null, 2);
			case 'vscode':
				return JSON.stringify({ servers: { synapbus: { type: "http", url, headers: { Authorization: `Bearer ${apiKey}` } } } }, null, 2);
			case 'claude-desktop':
				return JSON.stringify({ mcpServers: { synapbus: { command: "npx", args: ["mcp-remote", url, "--header", `Authorization: Bearer ${apiKey}`] } } }, null, 2);
		}
	}

	function getConfigFilePath(client: ClientId): string {
		switch (client) {
			case 'claude-code': return '.mcp.json or ~/.claude.json';
			case 'gemini': return '~/.gemini/settings.json';
			case 'cursor': return '.cursor/mcp.json';
			case 'windsurf': return '~/.codeium/windsurf/mcp_config.json';
			case 'vscode': return '.vscode/mcp.json';
			case 'claude-desktop': return '~/Library/Application Support/Claude/claude_desktop_config.json';
		}
	}

	let currentConfig = $derived(getConfigJson(selectedClient, authMode, newApiKey));
	let currentCli = $derived(getCliCommand(selectedClient, authMode, newApiKey));

	async function loadAgents() {
		loadingData = true;
		try {
			const res = await agentsApi.list();
			agentList = (res.agents || []).filter((a: any) => a.type !== 'human');
		} catch {
			// handled
		} finally {
			loadingData = false;
		}
	}

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			loadAgents();
		}
	});

	async function handleRegister(e: SubmitEvent) {
		e.preventDefault();
		if (!newName.trim()) {
			registerError = 'Agent name is required';
			return;
		}
		registering = true;
		registerError = '';
		newApiKey = '';
		try {
			const res = await agentsApi.register({
				name: newName.trim(),
				display_name: newDisplayName.trim() || undefined,
				type: 'ai'
			});
			newApiKey = res.api_key;
			await loadAgents();
		} catch (err: any) {
			registerError = err.message || 'Failed to register agent';
		} finally {
			registering = false;
		}
	}

	function resetForm() {
		showRegister = false;
		newApiKey = '';
		newName = '';
		newDisplayName = '';
		registerError = '';
		selectedClient = 'claude-code';
		authMode = 'apikey';
	}

	async function copyText(text: string, label: string) {
		try {
			await navigator.clipboard.writeText(text);
			copiedField = label;
			setTimeout(() => (copiedField = ''), 2000);
		} catch {
			// fallback
		}
	}
</script>

<div class="p-5 max-w-5xl">
	<div class="flex items-center justify-between mb-5">
		<h1 class="text-xl font-bold text-text-primary font-display">Agents</h1>
		<button class="btn-primary flex items-center gap-1.5" onclick={() => { if (showRegister) resetForm(); else showRegister = true; }}>
			{#if showRegister}
				Cancel
			{:else}
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
					<path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
				</svg>
				Register Agent
			{/if}
		</button>
	</div>

	{#if showRegister}
		{#if newApiKey}
			<!-- Success state: show credentials + MCP setup -->
			<div class="card p-5 mb-5">
				<div class="flex items-center gap-2 mb-4">
					<div class="w-8 h-8 rounded-full bg-accent-green/20 flex items-center justify-center">
						<svg class="w-5 h-5 text-accent-green" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
							<path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
						</svg>
					</div>
					<div>
						<h3 class="font-semibold text-sm text-text-primary font-display">Agent "{newName}" registered</h3>
						<p class="text-xs text-text-secondary">Save the API key below — it won't be shown again.</p>
					</div>
				</div>

				<!-- API Key -->
				<div class="mb-4">
					<div class="flex items-center justify-between mb-1.5">
						<label class="text-xs font-medium text-text-secondary">API Key</label>
						<button
							class="text-xs text-text-secondary hover:text-text-primary transition-colors"
							onclick={() => copyText(newApiKey, 'key')}
						>
							{copiedField === 'key' ? 'Copied!' : 'Copy'}
						</button>
					</div>
					<code class="block p-3 bg-bg-primary rounded text-xs font-mono text-accent-green break-all select-all border border-accent-green/20">{newApiKey}</code>
				</div>

				<!-- Client tabs -->
				<div class="mb-3">
					<label class="text-xs font-medium text-text-secondary mb-1.5 block">MCP Client</label>
					<div class="flex gap-1 flex-wrap">
						{#each clients as client (client.id)}
							<button
								class="px-2.5 py-1.5 rounded text-xs font-medium transition-colors {selectedClient === client.id ? 'bg-accent-green text-white' : 'bg-bg-tertiary text-text-secondary hover:text-text-primary hover:bg-bg-tertiary/80'}"
								onclick={() => (selectedClient = client.id)}
							>
								<span class="font-mono text-[10px] mr-1 opacity-70">{client.icon}</span>{client.label}
							</button>
						{/each}
					</div>
				</div>

				<!-- Auth mode switcher -->
				<div class="mb-4">
					<div class="flex items-center gap-2">
						<label class="text-xs font-medium text-text-secondary">Auth:</label>
						<div class="flex rounded overflow-hidden border border-border">
							<button
								class="px-3 py-1 text-xs transition-colors {authMode === 'apikey' ? 'bg-accent-green text-white' : 'bg-bg-tertiary text-text-secondary hover:text-text-primary'}"
								onclick={() => (authMode = 'apikey')}
							>API Key</button>
							<button
								class="px-3 py-1 text-xs transition-colors {authMode === 'oauth' ? 'bg-accent-green text-white' : 'bg-bg-tertiary text-text-secondary hover:text-text-primary'}"
								onclick={() => (authMode = 'oauth')}
							>OAuth</button>
						</div>
					</div>
				</div>

				<!-- CLI command (if available) -->
				{#if currentCli}
					<div class="mb-4">
						<div class="flex items-center justify-between mb-1.5">
							<label class="text-xs font-medium text-text-secondary">CLI Command</label>
							<button
								class="text-xs text-text-secondary hover:text-text-primary transition-colors"
								onclick={() => copyText(currentCli, 'cli')}
							>
								{copiedField === 'cli' ? 'Copied!' : 'Copy'}
							</button>
						</div>
						<pre class="p-3 bg-bg-primary rounded text-xs font-mono text-accent-green break-all select-all border border-accent-green/20 overflow-x-auto">{currentCli}</pre>
					</div>
				{/if}

				<!-- JSON config -->
				<div class="mb-4">
					<div class="flex items-center justify-between mb-1.5">
						<label class="text-xs font-medium text-text-secondary">JSON Configuration</label>
						<button
							class="text-xs text-text-secondary hover:text-text-primary transition-colors"
							onclick={() => copyText(currentConfig, 'config')}
						>
							{copiedField === 'config' ? 'Copied!' : 'Copy'}
						</button>
					</div>
					<pre class="p-3 bg-bg-primary rounded text-xs font-mono text-text-primary break-all select-all border border-border overflow-x-auto">{currentConfig}</pre>
					<p class="text-[10px] text-text-secondary mt-1.5">Add to <code class="font-mono">{getConfigFilePath(selectedClient)}</code></p>
				</div>

				<button
					class="btn-primary w-full"
					onclick={resetForm}
				>
					Done
				</button>
			</div>
		{:else}
			<!-- Registration form -->
			<form class="card p-5 mb-5" onsubmit={handleRegister}>
				<h3 class="font-semibold text-sm text-text-primary font-display mb-3">Register New Agent</h3>
				{#if registerError}
					<div class="mb-3 px-3 py-2 bg-accent-red/10 rounded text-xs text-accent-red">{registerError}</div>
				{/if}
				<div class="space-y-3">
					<div>
						<label for="agent-name" class="block text-xs font-medium text-text-secondary mb-1">Agent name</label>
						<input id="agent-name" type="text" class="input" placeholder="e.g. researcher-01" bind:value={newName} required />
					</div>
					<div>
						<label for="agent-display" class="block text-xs font-medium text-text-secondary mb-1">Display name <span class="text-text-secondary">(optional)</span></label>
						<input id="agent-display" type="text" class="input" placeholder="e.g. Research Agent" bind:value={newDisplayName} />
					</div>
					<button type="submit" class="btn-primary w-full" disabled={registering || !newName.trim()}>
						{registering ? 'Registering...' : 'Register Agent'}
					</button>
				</div>
			</form>
		{/if}
	{/if}

	{#if loadingData}
		<div class="grid gap-3 sm:grid-cols-2">
			{#each Array(4) as _}
				<div class="card p-4">
					<div class="flex gap-3">
						<div class="skeleton w-10 h-10 rounded-lg"></div>
						<div class="flex-1 space-y-2">
							<div class="skeleton h-4 w-1/3"></div>
							<div class="skeleton h-3 w-1/2"></div>
						</div>
					</div>
				</div>
			{/each}
		</div>
	{:else if agentList.length === 0 && !showRegister}
		<div class="card p-8 text-center">
			<p class="text-text-secondary text-sm mb-3">No agents registered yet.</p>
			<button class="btn-primary" onclick={() => (showRegister = true)}>Register your first agent</button>
		</div>
	{:else}
		<div class="grid gap-3 sm:grid-cols-2">
			{#each agentList as agent (agent.id)}
				<AgentCard {agent} ownerName={$user?.display_name || $user?.username} />
			{/each}
		</div>
	{/if}
</div>

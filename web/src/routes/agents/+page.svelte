<script lang="ts">
	import { agents as agentsApi } from '$lib/api/client';
	import { user } from '$lib/stores/auth';
	import AgentCard from '$lib/components/AgentCard.svelte';

	let agentList = $state<any[]>([]);
	let loadingData = $state(true);
	let showRegister = $state(false);

	let newName = $state('');
	let newDisplayName = $state('');
	let newType = 'ai'; // agents are always AI; human accounts created via CLI
	let registering = $state(false);
	let registerError = $state('');
	let newApiKey = $state('');
	let copiedField = $state('');

	async function loadAgents() {
		loadingData = true;
		try {
			const res = await agentsApi.list();
			agentList = res.agents;
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
				type: newType
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
		newType = 'ai';
		registerError = '';
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

	let mcpConfig = $derived(newApiKey ? JSON.stringify({
		mcpServers: {
			synapbus: {
				url: `${typeof window !== 'undefined' ? window.location.origin : 'http://localhost:8080'}/mcp`,
				headers: {
					Authorization: `Bearer ${newApiKey}`
				}
			}
		}
	}, null, 2) : '');
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
			<!-- Success state: show credentials -->
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

				<!-- MCP Config -->
				<div class="mb-4">
					<div class="flex items-center justify-between mb-1.5">
						<label class="text-xs font-medium text-text-secondary">MCP Configuration</label>
						<button
							class="text-xs text-text-secondary hover:text-text-primary transition-colors"
							onclick={() => copyText(mcpConfig, 'mcp')}
						>
							{copiedField === 'mcp' ? 'Copied!' : 'Copy'}
						</button>
					</div>
					<pre class="p-3 bg-bg-primary rounded text-xs font-mono text-text-primary break-all select-all border border-border overflow-x-auto">{mcpConfig}</pre>
					<p class="text-[10px] text-text-secondary mt-1.5">Add this to your agent's MCP configuration (e.g. claude_desktop_config.json)</p>
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

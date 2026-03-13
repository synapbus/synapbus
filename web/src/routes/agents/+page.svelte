<script lang="ts">
	import { agents as agentsApi } from '$lib/api/client';
	import AgentCard from '$lib/components/AgentCard.svelte';

	let agentList = $state<any[]>([]);
	let loadingData = $state(true);
	let showRegister = $state(false);

	let newName = $state('');
	let newDisplayName = $state('');
	let newType = $state('ai');
	let registering = $state(false);
	let registerError = $state('');
	let newApiKey = $state('');

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
			newName = '';
			newDisplayName = '';
			newType = 'ai';
			await loadAgents();
		} catch (err: any) {
			registerError = err.message || 'Failed to register agent';
		} finally {
			registering = false;
		}
	}
</script>

<div class="p-5 max-w-5xl">
	<div class="flex items-center justify-between mb-5">
		<h1 class="text-xl font-bold text-text-primary font-display">Agents</h1>
		<button class="btn-primary flex items-center gap-1.5" onclick={() => { showRegister = !showRegister; newApiKey = ''; }}>
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
		<form class="card p-5 mb-5" onsubmit={handleRegister}>
			<h3 class="font-semibold text-sm text-text-primary font-display mb-3">Register New Agent</h3>
			{#if registerError}
				<div class="mb-3 px-3 py-2 bg-accent-red/10 rounded text-xs text-accent-red">{registerError}</div>
			{/if}
			{#if newApiKey}
				<div class="mb-4 p-4 bg-accent-green/10 border border-accent-green/20 rounded-lg">
					<p class="text-xs font-semibold text-accent-green mb-2">Agent registered! Save this API key - it will not be shown again:</p>
					<code class="block p-3 bg-bg-primary rounded text-xs font-mono text-text-primary break-all select-all border border-border">{newApiKey}</code>
				</div>
			{/if}
			<div class="space-y-3">
				<input type="text" class="input" placeholder="Agent name (e.g. researcher-01)" bind:value={newName} />
				<input type="text" class="input" placeholder="Display name (optional)" bind:value={newDisplayName} />
				<select class="input" bind:value={newType}>
					<option value="ai">AI Agent</option>
					<option value="human">Human Agent</option>
				</select>
				<button type="submit" class="btn-primary" disabled={registering}>
					{registering ? 'Registering...' : 'Register'}
				</button>
			</div>
		</form>
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
	{:else if agentList.length === 0}
		<div class="card p-8 text-center text-text-secondary text-sm">
			No agents registered yet.
		</div>
	{:else}
		<div class="grid gap-3 sm:grid-cols-2">
			{#each agentList as agent (agent.id)}
				<AgentCard {agent} />
			{/each}
		</div>
	{/if}
</div>

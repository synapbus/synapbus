<script lang="ts">
	import { onMount } from 'svelte';
	import { agents as agentsApi } from '$lib/api/client';
	import AgentCard from '$lib/components/AgentCard.svelte';

	let agentList = $state<any[]>([]);
	let loadingData = $state(true);
	let showRegister = $state(false);

	// Register form
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

	onMount(loadAgents);

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

<div class="max-w-4xl mx-auto">
	<div class="flex items-center justify-between mb-6">
		<h1 class="text-2xl font-bold text-gray-900 dark:text-white">Agents</h1>
		<button class="btn-primary" onclick={() => { showRegister = !showRegister; newApiKey = ''; }}>
			{showRegister ? 'Cancel' : 'Register Agent'}
		</button>
	</div>

	{#if showRegister}
		<form class="card p-4 mb-6" onsubmit={handleRegister}>
			<h3 class="font-medium text-gray-900 dark:text-gray-100 mb-3">Register New Agent</h3>
			{#if registerError}
				<div class="mb-3 p-2 bg-red-50 dark:bg-red-900/30 rounded text-sm text-red-700 dark:text-red-300">{registerError}</div>
			{/if}
			{#if newApiKey}
				<div class="mb-3 p-3 bg-green-50 dark:bg-green-900/30 border border-green-200 dark:border-green-800 rounded-lg">
					<p class="text-sm font-medium text-green-800 dark:text-green-200 mb-1">Agent registered! Save this API key - it will not be shown again:</p>
					<code class="block p-2 bg-white dark:bg-gray-800 rounded text-xs font-mono break-all select-all">{newApiKey}</code>
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
		<div class="grid gap-4 sm:grid-cols-2">
			{#each Array(4) as _}
				<div class="card p-4">
					<div class="skeleton h-5 w-1/3 mb-2"></div>
					<div class="skeleton h-4 w-1/2"></div>
				</div>
			{/each}
		</div>
	{:else if agentList.length === 0}
		<div class="card p-8 text-center">
			<p class="text-gray-500 dark:text-gray-400">No agents registered yet.</p>
		</div>
	{:else}
		<div class="grid gap-4 sm:grid-cols-2">
			{#each agentList as agent (agent.id)}
				<AgentCard {agent} />
			{/each}
		</div>
	{/if}
</div>

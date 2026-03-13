<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { agents as agentsApi } from '$lib/api/client';
	import TraceViewer from '$lib/components/TraceViewer.svelte';

	let agent = $state<any>(null);
	let traces = $state<any[]>([]);
	let loadingData = $state(true);
	let revokeKey = $state('');
	let revoking = $state(false);
	let deleting = $state(false);
	let confirmDelete = $state(false);

	$: agentName = $page.params.name;

	async function loadAgent() {
		loadingData = true;
		try {
			const res = await agentsApi.get(agentName);
			agent = res.agent;
			traces = res.traces;
		} catch {
			// handled
		} finally {
			loadingData = false;
		}
	}

	onMount(loadAgent);

	async function handleRevokeKey() {
		revoking = true;
		revokeKey = '';
		try {
			const res = await agentsApi.revokeKey(agentName);
			revokeKey = res.api_key;
			agent = res.agent;
		} catch (err: any) {
			alert(err.message || 'Failed to revoke key');
		} finally {
			revoking = false;
		}
	}

	async function handleDelete() {
		deleting = true;
		try {
			await agentsApi.delete(agentName);
			goto('/agents');
		} catch (err: any) {
			alert(err.message || 'Failed to delete agent');
		} finally {
			deleting = false;
			confirmDelete = false;
		}
	}
</script>

<div class="max-w-4xl mx-auto">
	<a href="/agents" class="text-sm text-primary-600 hover:text-primary-700 dark:text-primary-400 mb-4 inline-block">
		&larr; Back to agents
	</a>

	{#if loadingData}
		<div class="card p-6">
			<div class="skeleton h-8 w-1/3 mb-4"></div>
			<div class="skeleton h-4 w-1/2 mb-2"></div>
			<div class="skeleton h-4 w-1/4"></div>
		</div>
	{:else if agent}
		<!-- Agent Details -->
		<div class="card mb-6">
			<div class="p-4 border-b border-gray-200 dark:border-gray-700">
				<div class="flex items-start justify-between">
					<div>
						<h1 class="text-xl font-bold text-gray-900 dark:text-white">{agent.display_name || agent.name}</h1>
						{#if agent.display_name}
							<p class="text-sm text-gray-500 dark:text-gray-400">@{agent.name}</p>
						{/if}
					</div>
					<div class="flex items-center gap-2">
						<span class="badge {agent.type === 'ai' ? 'bg-purple-100 text-purple-700 dark:bg-purple-900 dark:text-purple-300' : 'bg-blue-100 text-blue-700 dark:bg-blue-900 dark:text-blue-300'}">
							{agent.type}
						</span>
						<span class="badge {agent.status === 'active' ? 'bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300' : 'bg-gray-100 text-gray-600'}">
							{agent.status}
						</span>
					</div>
				</div>
			</div>

			<div class="p-4 space-y-3">
				<div class="grid grid-cols-2 gap-4 text-sm">
					<div>
						<p class="text-gray-500 dark:text-gray-400">Created</p>
						<p class="text-gray-900 dark:text-gray-100">{new Date(agent.created_at).toLocaleString()}</p>
					</div>
					<div>
						<p class="text-gray-500 dark:text-gray-400">Last Updated</p>
						<p class="text-gray-900 dark:text-gray-100">{new Date(agent.updated_at).toLocaleString()}</p>
					</div>
				</div>

				{#if agent.capabilities && JSON.stringify(agent.capabilities) !== '{}'}
					<div>
						<p class="text-sm text-gray-500 dark:text-gray-400 mb-1">Capabilities</p>
						<pre class="text-xs bg-gray-50 dark:bg-gray-900 p-2 rounded overflow-x-auto">{JSON.stringify(agent.capabilities, null, 2)}</pre>
					</div>
				{/if}
			</div>

			<!-- Actions -->
			<div class="p-4 border-t border-gray-200 dark:border-gray-700 space-y-3">
				{#if revokeKey}
					<div class="p-3 bg-green-50 dark:bg-green-900/30 border border-green-200 dark:border-green-800 rounded-lg">
						<p class="text-sm font-medium text-green-800 dark:text-green-200 mb-1">New API key generated. Save it now - it will not be shown again:</p>
						<code class="block p-2 bg-white dark:bg-gray-800 rounded text-xs font-mono break-all select-all">{revokeKey}</code>
					</div>
				{/if}

				<div class="flex gap-2">
					<button class="btn-secondary text-sm" onclick={handleRevokeKey} disabled={revoking}>
						{revoking ? 'Regenerating...' : 'Regenerate API Key'}
					</button>
					{#if !confirmDelete}
						<button class="btn-danger text-sm" onclick={() => (confirmDelete = true)}>
							Delete Agent
						</button>
					{:else}
						<button class="btn-danger text-sm" onclick={handleDelete} disabled={deleting}>
							{deleting ? 'Deleting...' : 'Confirm Delete'}
						</button>
						<button class="btn-secondary text-sm" onclick={() => (confirmDelete = false)}>Cancel</button>
					{/if}
				</div>
			</div>
		</div>

		<!-- Activity Traces -->
		<div class="card">
			<div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
				<h2 class="font-semibold text-gray-900 dark:text-gray-100">Activity Traces</h2>
			</div>
			<TraceViewer {traces} />
		</div>
	{:else}
		<div class="card p-8 text-center">
			<p class="text-gray-500 dark:text-gray-400">Agent not found</p>
		</div>
	{/if}
</div>

<script lang="ts">
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

	let agentName = $derived($page.params.name);

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

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			loadAgent();
		}
	});

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

<div class="p-5 max-w-5xl">
	<a href="/agents" class="inline-flex items-center gap-1 text-xs text-text-link hover:underline mb-4">
		<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
			<path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
		</svg>
		Back to agents
	</a>

	{#if loadingData}
		<div class="card p-6">
			<div class="flex gap-3">
				<div class="skeleton w-12 h-12 rounded-lg"></div>
				<div class="flex-1 space-y-2">
					<div class="skeleton h-5 w-1/3"></div>
					<div class="skeleton h-3 w-1/4"></div>
				</div>
			</div>
		</div>
	{:else if agent}
		<!-- Agent Details -->
		<div class="card mb-5">
			<div class="p-5 border-b border-border">
				<div class="flex items-start justify-between">
					<div class="flex items-center gap-3">
						<div class="w-12 h-12 rounded-lg bg-bg-tertiary flex items-center justify-center text-lg font-bold text-text-secondary">
							{(agent.display_name || agent.name).charAt(0).toUpperCase()}
						</div>
						<div>
							<h1 class="text-lg font-bold text-text-primary font-display">{agent.display_name || agent.name}</h1>
							{#if agent.display_name}
								<p class="text-xs text-text-secondary font-mono">@{agent.name}</p>
							{/if}
						</div>
					</div>
					<div class="flex items-center gap-2">
						<span class="badge {agent.type === 'ai' ? 'bg-accent-purple/20 text-accent-purple' : 'bg-accent-blue/20 text-accent-blue'}">
							{agent.type}
						</span>
						<span class="flex items-center gap-1.5 badge {agent.status === 'active' ? 'bg-accent-green/20 text-accent-green' : 'bg-bg-tertiary text-text-secondary'}">
							<span class="w-1.5 h-1.5 rounded-full {agent.status === 'active' ? 'bg-accent-green' : 'bg-text-secondary'}"></span>
							{agent.status}
						</span>
					</div>
				</div>
			</div>

			<div class="p-5 space-y-3">
				<div class="grid grid-cols-2 gap-4 text-sm">
					<div>
						<p class="text-xs text-text-secondary mb-0.5">Created</p>
						<p class="text-text-primary">{new Date(agent.created_at).toLocaleString()}</p>
					</div>
					<div>
						<p class="text-xs text-text-secondary mb-0.5">Last Updated</p>
						<p class="text-text-primary">{new Date(agent.updated_at).toLocaleString()}</p>
					</div>
				</div>

				{#if agent.capabilities && JSON.stringify(agent.capabilities) !== '{}'}
					<div>
						<p class="text-xs text-text-secondary mb-1">Capabilities</p>
						<pre class="text-xs bg-bg-primary p-3 rounded-lg border border-border overflow-x-auto font-mono text-text-primary/80">{JSON.stringify(agent.capabilities, null, 2)}</pre>
					</div>
				{/if}
			</div>

			<!-- Actions -->
			<div class="p-5 border-t border-border space-y-3">
				{#if revokeKey}
					<div class="p-4 bg-accent-green/10 border border-accent-green/20 rounded-lg">
						<p class="text-xs font-semibold text-accent-green mb-2">New API key generated. Save it now - it will not be shown again:</p>
						<code class="block p-3 bg-bg-primary rounded text-xs font-mono text-text-primary break-all select-all border border-border">{revokeKey}</code>
					</div>
				{/if}

				<div class="flex gap-2">
					<button class="btn-secondary text-xs" onclick={handleRevokeKey} disabled={revoking}>
						{revoking ? 'Regenerating...' : 'Regenerate API Key'}
					</button>
					{#if !confirmDelete}
						<button class="btn-danger text-xs" onclick={() => (confirmDelete = true)}>
							Delete Agent
						</button>
					{:else}
						<button class="btn-danger text-xs" onclick={handleDelete} disabled={deleting}>
							{deleting ? 'Deleting...' : 'Confirm Delete'}
						</button>
						<button class="btn-secondary text-xs" onclick={() => (confirmDelete = false)}>Cancel</button>
					{/if}
				</div>
			</div>
		</div>

		<!-- Activity Traces -->
		<div class="card">
			<div class="px-5 py-3 border-b border-border">
				<h2 class="font-semibold text-sm text-text-primary font-display">Activity Traces</h2>
			</div>
			<TraceViewer {traces} />
		</div>
	{:else}
		<div class="card p-8 text-center text-text-secondary text-sm">
			Agent not found
		</div>
	{/if}
</div>

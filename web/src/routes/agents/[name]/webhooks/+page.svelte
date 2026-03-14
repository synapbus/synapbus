<script lang="ts">
	import { page } from '$app/stores';
	import { webhooks as webhooksApi } from '$lib/api/client';

	let hooksList = $state<any[]>([]);
	let deliveries = $state<any[]>([]);
	let loading = $state(true);
	let selectedWebhook = $state<number | null>(null);
	let statusFilter = $state('');
	let togglingId = $state<number | null>(null);

	let agentName = $derived($page.params.name);

	async function loadWebhooks() {
		loading = true;
		try {
			const res = await webhooksApi.list(agentName);
			hooksList = res.webhooks || [];
		} catch {
			// handled
		} finally {
			loading = false;
		}
	}

	async function loadDeliveries(webhookId: number) {
		selectedWebhook = webhookId;
		try {
			const res = await webhooksApi.deliveries(webhookId, { status: statusFilter, limit: 50 });
			deliveries = res.deliveries || [];
		} catch {
			deliveries = [];
		}
	}

	async function toggleWebhook(id: number, currentStatus: string) {
		togglingId = id;
		try {
			if (currentStatus === 'active') {
				await webhooksApi.disable(id);
			} else {
				await webhooksApi.enable(id);
			}
			await loadWebhooks();
			if (selectedWebhook === id) await loadDeliveries(id);
		} catch (err: any) {
			alert(err.message || 'Failed to toggle webhook');
		} finally {
			togglingId = null;
		}
	}

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			loadWebhooks();
		}
	});

	function statusBadge(status: string): string {
		switch (status) {
			case 'active': return 'bg-accent-green/20 text-accent-green';
			case 'disabled': return 'bg-accent-red/20 text-accent-red';
			case 'delivered': return 'bg-accent-green/20 text-accent-green';
			case 'pending': return 'bg-accent-yellow/20 text-accent-yellow';
			case 'retrying': return 'bg-accent-blue/20 text-accent-blue';
			case 'dead_lettered': return 'bg-accent-red/20 text-accent-red';
			default: return 'bg-bg-tertiary text-text-secondary';
		}
	}
</script>

<div class="p-5 max-w-5xl">
	<a href="/agents/{agentName}" class="inline-flex items-center gap-1 text-xs text-text-link hover:underline mb-4">
		<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
			<path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
		</svg>
		Back to {agentName}
	</a>

	<h1 class="text-lg font-bold text-text-primary font-display mb-4">Webhooks for @{agentName}</h1>

	{#if loading}
		<div class="card p-6">
			<div class="skeleton h-4 w-1/3 mb-3"></div>
			<div class="skeleton h-4 w-2/3"></div>
		</div>
	{:else if hooksList.length === 0}
		<div class="card p-8 text-center text-text-secondary text-sm">
			No webhooks registered for this agent. Use the <code class="font-mono bg-bg-tertiary px-1 rounded">register_webhook</code> MCP tool to add one.
		</div>
	{:else}
		<div class="space-y-3 mb-6">
			{#each hooksList as wh}
				<div class="card">
					<div class="p-4 flex items-center justify-between">
						<div class="flex-1 min-w-0">
							<div class="flex items-center gap-2 mb-1">
								<span class="badge {statusBadge(wh.status)}">{wh.status}</span>
								<code class="text-xs font-mono text-text-primary truncate">{wh.url}</code>
							</div>
							<div class="flex items-center gap-3 text-[10px] text-text-secondary">
								<span>Events: {(wh.events || []).join(', ')}</span>
								{#if wh.consecutive_failures > 0}
									<span class="text-accent-red">Failures: {wh.consecutive_failures}</span>
								{/if}
								<span>Created: {new Date(wh.created_at).toLocaleDateString()}</span>
							</div>
						</div>
						<div class="flex items-center gap-2 ml-4">
							<button
								class="btn-secondary text-xs"
								onclick={() => loadDeliveries(wh.id)}
								class:ring-2={selectedWebhook === wh.id}
								class:ring-accent-blue={selectedWebhook === wh.id}
							>
								Deliveries
							</button>
							<button
								class="text-xs {wh.status === 'active' ? 'btn-danger' : 'btn-primary'}"
								onclick={() => toggleWebhook(wh.id, wh.status)}
								disabled={togglingId === wh.id}
							>
								{togglingId === wh.id ? '...' : wh.status === 'active' ? 'Disable' : 'Enable'}
							</button>
						</div>
					</div>
				</div>
			{/each}
		</div>

		<!-- Delivery History -->
		{#if selectedWebhook !== null}
			<div class="card">
				<div class="px-5 py-3 border-b border-border flex items-center justify-between">
					<h2 class="font-semibold text-sm text-text-primary font-display">Delivery History</h2>
					<select
						class="input text-xs w-auto"
						bind:value={statusFilter}
						onchange={() => loadDeliveries(selectedWebhook!)}
					>
						<option value="">All</option>
						<option value="delivered">Delivered</option>
						<option value="pending">Pending</option>
						<option value="retrying">Retrying</option>
						<option value="dead_lettered">Dead Letter</option>
					</select>
				</div>
				{#if deliveries.length === 0}
					<div class="p-6 text-center text-text-secondary text-xs">No deliveries found</div>
				{:else}
					<div class="overflow-x-auto">
						<table class="w-full text-xs">
							<thead>
								<tr class="border-b border-border text-text-secondary">
									<th class="px-4 py-2 text-left font-medium">ID</th>
									<th class="px-4 py-2 text-left font-medium">Event</th>
									<th class="px-4 py-2 text-left font-medium">Status</th>
									<th class="px-4 py-2 text-left font-medium">HTTP</th>
									<th class="px-4 py-2 text-left font-medium">Attempts</th>
									<th class="px-4 py-2 text-left font-medium">Error</th>
									<th class="px-4 py-2 text-left font-medium">Time</th>
								</tr>
							</thead>
							<tbody>
								{#each deliveries as d}
									<tr class="border-b border-border/50 hover:bg-bg-tertiary/30">
										<td class="px-4 py-2 font-mono">{d.id}</td>
										<td class="px-4 py-2">{d.event}</td>
										<td class="px-4 py-2">
											<span class="badge {statusBadge(d.status)}">{d.status}</span>
										</td>
										<td class="px-4 py-2">{d.http_status || '-'}</td>
										<td class="px-4 py-2">{d.attempts}/{d.max_attempts}</td>
										<td class="px-4 py-2 text-accent-red max-w-[200px] truncate">{d.last_error || '-'}</td>
										<td class="px-4 py-2 text-text-secondary">{new Date(d.created_at).toLocaleString()}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			</div>
		{/if}
	{/if}
</div>

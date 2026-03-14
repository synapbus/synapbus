<script lang="ts">
	import { webhookDeliveries } from '$lib/api/client';

	let deadLetters = $state<any[]>([]);
	let loading = $state(true);
	let retryingId = $state<number | null>(null);

	async function loadDeadLetters() {
		loading = true;
		try {
			const res = await webhookDeliveries.deadLetters({ limit: 100 });
			deadLetters = res.deliveries || [];
		} catch {
			// handled
		} finally {
			loading = false;
		}
	}

	async function retryDelivery(id: number) {
		retryingId = id;
		try {
			await webhookDeliveries.retry(id);
			await loadDeadLetters();
		} catch (err: any) {
			alert(err.message || 'Failed to retry delivery');
		} finally {
			retryingId = null;
		}
	}

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			loadDeadLetters();
		}
	});
</script>

<div class="p-5 max-w-5xl">
	<a href="/dead-letters" class="inline-flex items-center gap-1 text-xs text-text-link hover:underline mb-4">
		<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
			<path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
		</svg>
		Back to dead letters
	</a>

	<h1 class="text-lg font-bold text-text-primary font-display mb-4">Webhook Dead Letters</h1>
	<p class="text-xs text-text-secondary mb-4">Webhook deliveries that failed after all retry attempts.</p>

	{#if loading}
		<div class="card p-6">
			<div class="skeleton h-4 w-1/3 mb-3"></div>
			<div class="skeleton h-4 w-2/3 mb-3"></div>
			<div class="skeleton h-4 w-1/2"></div>
		</div>
	{:else if deadLetters.length === 0}
		<div class="card p-8 text-center text-text-secondary text-sm">
			No dead-lettered webhook deliveries. All deliveries succeeded or are still retrying.
		</div>
	{:else}
		<div class="card">
			<div class="overflow-x-auto">
				<table class="w-full text-xs">
					<thead>
						<tr class="border-b border-border text-text-secondary">
							<th class="px-4 py-2 text-left font-medium">ID</th>
							<th class="px-4 py-2 text-left font-medium">Agent</th>
							<th class="px-4 py-2 text-left font-medium">Event</th>
							<th class="px-4 py-2 text-left font-medium">Message</th>
							<th class="px-4 py-2 text-left font-medium">HTTP</th>
							<th class="px-4 py-2 text-left font-medium">Attempts</th>
							<th class="px-4 py-2 text-left font-medium">Error</th>
							<th class="px-4 py-2 text-left font-medium">Time</th>
							<th class="px-4 py-2 text-left font-medium">Actions</th>
						</tr>
					</thead>
					<tbody>
						{#each deadLetters as d}
							<tr class="border-b border-border/50 hover:bg-bg-tertiary/30">
								<td class="px-4 py-2 font-mono">{d.id}</td>
								<td class="px-4 py-2">@{d.agent_name}</td>
								<td class="px-4 py-2">{d.event}</td>
								<td class="px-4 py-2">#{d.message_id}</td>
								<td class="px-4 py-2">{d.http_status || '-'}</td>
								<td class="px-4 py-2">{d.attempts}/{d.max_attempts}</td>
								<td class="px-4 py-2 text-accent-red max-w-[250px] truncate" title={d.last_error}>
									{d.last_error || '-'}
								</td>
								<td class="px-4 py-2 text-text-secondary">{new Date(d.created_at).toLocaleString()}</td>
								<td class="px-4 py-2">
									<button
										class="btn-primary text-[10px] px-2 py-0.5"
										onclick={() => retryDelivery(d.id)}
										disabled={retryingId === d.id}
									>
										{retryingId === d.id ? '...' : 'Retry'}
									</button>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		</div>
	{/if}
</div>

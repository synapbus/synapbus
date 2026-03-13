<script lang="ts">
	import { page } from '$app/stores';
	import { conversations as convsApi, messages as messagesApi } from '$lib/api/client';

	let convList = $state<any[]>([]);
	let searchResults = $state<any[] | null>(null);
	let loadingData = $state(true);

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			(async () => {
				const q = $page.url.searchParams.get('q');
				if (q) {
					try {
						const res = await messagesApi.search(q);
						searchResults = res.messages;
					} catch {
						searchResults = [];
					}
				}

				try {
					const res = await convsApi.list();
					convList = res.conversations;
				} catch {
					// handled
				} finally {
					loadingData = false;
				}
			})();
		}
	});
</script>

<div class="p-5 max-w-5xl">
	<h1 class="text-xl font-bold text-text-primary font-display mb-5">Conversations</h1>

	{#if searchResults !== null}
		<div class="card mb-5">
			<div class="px-5 py-3 border-b border-border">
				<h2 class="font-semibold text-sm text-text-primary font-display">
					Search Results ({searchResults.length})
				</h2>
			</div>
			{#if searchResults.length === 0}
				<div class="p-8 text-center text-text-secondary text-sm">
					No messages found for '{$page.url.searchParams.get('q')}'
				</div>
			{:else}
				<div class="divide-y divide-border">
					{#each searchResults as msg}
						<a href="/conversations/{msg.conversation_id}" class="block px-5 py-3 hover:bg-bg-tertiary/50 transition-colors">
							<div class="flex items-center gap-2 mb-1">
								<span class="font-semibold text-sm text-text-primary">{msg.from_agent}</span>
								<span class="text-xs text-text-secondary">{new Date(msg.created_at).toLocaleString()}</span>
							</div>
							<p class="text-sm text-text-primary/80">{msg.body.slice(0, 200)}</p>
						</a>
					{/each}
				</div>
			{/if}
		</div>
	{/if}

	<div class="card">
		<div class="px-5 py-3 border-b border-border">
			<h2 class="font-semibold text-sm text-text-primary font-display">All Conversations</h2>
		</div>
		{#if loadingData}
			<div class="p-5 space-y-4">
				{#each Array(5) as _}
					<div class="space-y-2">
						<div class="skeleton h-3 w-1/3"></div>
						<div class="skeleton h-3 w-2/3"></div>
					</div>
				{/each}
			</div>
		{:else if convList.length === 0}
			<div class="p-8 text-center text-text-secondary text-sm">
				No conversations yet.
			</div>
		{:else}
			<div class="divide-y divide-border">
				{#each convList as conv}
					<a href="/conversations/{conv.id}" class="block px-5 py-3 hover:bg-bg-tertiary/50 transition-colors">
						<div class="flex items-center justify-between">
							<div class="min-w-0">
								<p class="font-medium text-sm text-text-primary truncate">
									{conv.subject || 'Untitled'}
								</p>
								<p class="text-xs text-text-secondary truncate mt-0.5">
									{conv.last_agent}: {conv.last_message}
								</p>
							</div>
							<span class="badge bg-bg-tertiary text-text-secondary flex-shrink-0 ml-3">
								{conv.message_count}
							</span>
						</div>
					</a>
				{/each}
			</div>
		{/if}
	</div>
</div>

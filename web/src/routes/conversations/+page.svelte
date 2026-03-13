<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { conversations as convsApi, messages as messagesApi } from '$lib/api/client';

	let convList = $state<any[]>([]);
	let searchResults = $state<any[] | null>(null);
	let loadingData = $state(true);

	onMount(async () => {
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
	});
</script>

<div class="max-w-4xl mx-auto">
	<h1 class="text-2xl font-bold text-gray-900 dark:text-white mb-6">Conversations</h1>

	{#if searchResults !== null}
		<div class="card mb-6">
			<div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
				<h2 class="font-semibold text-gray-900 dark:text-gray-100">
					Search Results ({searchResults.length})
				</h2>
			</div>
			{#if searchResults.length === 0}
				<div class="p-8 text-center text-gray-500 dark:text-gray-400">
					No messages found for '{$page.url.searchParams.get('q')}'
				</div>
			{:else}
				<div class="divide-y divide-gray-100 dark:divide-gray-700">
					{#each searchResults as msg}
						<a href="/conversations/{msg.conversation_id}" class="block px-4 py-3 hover:bg-gray-50 dark:hover:bg-gray-700/50">
							<div class="flex items-center gap-2 mb-1">
								<span class="font-medium text-sm">{msg.from_agent}</span>
								<span class="text-xs text-gray-500">{new Date(msg.created_at).toLocaleString()}</span>
							</div>
							<p class="text-sm text-gray-700 dark:text-gray-300">{msg.body.slice(0, 200)}</p>
						</a>
					{/each}
				</div>
			{/if}
		</div>
	{/if}

	<div class="card">
		<div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
			<h2 class="font-semibold text-gray-900 dark:text-gray-100">All Conversations</h2>
		</div>
		{#if loadingData}
			<div class="p-4 space-y-3">
				{#each Array(5) as _}
					<div class="space-y-2">
						<div class="skeleton h-4 w-1/3"></div>
						<div class="skeleton h-3 w-2/3"></div>
					</div>
				{/each}
			</div>
		{:else if convList.length === 0}
			<div class="p-8 text-center text-gray-500 dark:text-gray-400">
				<p>No conversations yet.</p>
			</div>
		{:else}
			<div class="divide-y divide-gray-100 dark:divide-gray-700">
				{#each convList as conv}
					<a href="/conversations/{conv.id}" class="block px-4 py-3 hover:bg-gray-50 dark:hover:bg-gray-700/50 transition-colors">
						<div class="flex items-center justify-between">
							<div class="min-w-0">
								<p class="font-medium text-sm text-gray-900 dark:text-gray-100 truncate">
									{conv.subject || 'Untitled'}
								</p>
								<p class="text-xs text-gray-500 dark:text-gray-400 truncate mt-0.5">
									{conv.last_agent}: {conv.last_message}
								</p>
							</div>
							<span class="badge bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-400 flex-shrink-0 ml-3">
								{conv.message_count}
							</span>
						</div>
					</a>
				{/each}
			</div>
		{/if}
	</div>
</div>

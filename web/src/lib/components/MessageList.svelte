<script lang="ts">
	type Message = {
		id: number;
		conversation_id: number;
		from_agent: string;
		to_agent?: string;
		body: string;
		priority: number;
		status: string;
		created_at: string;
	};

	let { messages = [], showConversationLink = false }: { messages: Message[]; showConversationLink?: boolean } = $props();

	function statusClass(status: string): string {
		switch (status) {
			case 'pending': return 'badge-pending';
			case 'processing': return 'badge-processing';
			case 'done': return 'badge-done';
			case 'failed': return 'badge-failed';
			default: return 'badge bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-300';
		}
	}

	function formatTime(iso: string): string {
		const d = new Date(iso);
		const now = new Date();
		const diff = now.getTime() - d.getTime();
		if (diff < 60000) return 'just now';
		if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
		if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
		return d.toLocaleDateString();
	}

	function truncate(s: string, len: number): string {
		return s.length > len ? s.slice(0, len - 3) + '...' : s;
	}
</script>

{#if messages.length === 0}
	<div class="text-center py-12 text-gray-500 dark:text-gray-400">
		<svg class="w-12 h-12 mx-auto mb-4 text-gray-300 dark:text-gray-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
			<path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4" />
		</svg>
		<p>No messages yet</p>
	</div>
{:else}
	<div class="divide-y divide-gray-100 dark:divide-gray-700">
		{#each messages as msg (msg.id)}
			<div class="px-4 py-3 hover:bg-gray-50 dark:hover:bg-gray-700/50 transition-colors">
				<div class="flex items-start justify-between gap-3">
					<div class="min-w-0 flex-1">
						<div class="flex items-center gap-2 mb-1">
							<span class="font-medium text-sm text-gray-900 dark:text-gray-100">{msg.from_agent}</span>
							{#if msg.to_agent}
								<svg class="w-3 h-3 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
									<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 7l5 5m0 0l-5 5m5-5H6" />
								</svg>
								<span class="text-sm text-gray-600 dark:text-gray-400">{msg.to_agent}</span>
							{/if}
							<span class="{statusClass(msg.status)}">{msg.status}</span>
							{#if msg.priority >= 8}
								<span class="badge bg-red-100 text-red-700 dark:bg-red-900 dark:text-red-300">P{msg.priority}</span>
							{:else if msg.priority >= 5}
								<span class="badge bg-orange-100 text-orange-700 dark:bg-orange-900 dark:text-orange-300">P{msg.priority}</span>
							{/if}
						</div>
						<p class="text-sm text-gray-700 dark:text-gray-300">{truncate(msg.body, 200)}</p>
					</div>
					<div class="flex flex-col items-end gap-1 flex-shrink-0">
						<span class="text-xs text-gray-500 dark:text-gray-400">{formatTime(msg.created_at)}</span>
						{#if showConversationLink}
							<a href="/conversations/{msg.conversation_id}" class="text-xs text-primary-600 hover:text-primary-700 dark:text-primary-400">
								View thread
							</a>
						{/if}
					</div>
				</div>
			</div>
		{/each}
	</div>
{/if}

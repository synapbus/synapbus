<script lang="ts">
	import { openThread } from '$lib/stores/thread';

	type Message = {
		id: number;
		conversation_id: number;
		from_agent: string;
		to_agent?: string;
		body: string;
		priority: number;
		status: string;
		created_at: string;
		reply_count?: number;
	};

	let { messages = [], showConversationLink = false }: { messages: Message[]; showConversationLink?: boolean } = $props();

	let hoveredId = $state<number | null>(null);

	function statusClass(status: string): string {
		switch (status) {
			case 'pending': return 'badge-pending';
			case 'processing': return 'badge-processing';
			case 'done': return 'badge-done';
			case 'failed': return 'badge-failed';
			default: return 'badge bg-bg-tertiary text-text-secondary';
		}
	}

	function formatTime(iso: string): string {
		const d = new Date(iso);
		const now = new Date();
		const diff = now.getTime() - d.getTime();
		if (diff < 60000) return 'just now';
		if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
		if (diff < 86400000) {
			return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
		}
		return d.toLocaleDateString([], { month: 'short', day: 'numeric' });
	}

	function agentColor(name: string): string {
		const colors = ['bg-accent-blue', 'bg-accent-green', 'bg-accent-purple', 'bg-accent-yellow', 'bg-accent-red'];
		let hash = 0;
		for (let i = 0; i < name.length; i++) {
			hash = name.charCodeAt(i) + ((hash << 5) - hash);
		}
		return colors[Math.abs(hash) % colors.length];
	}

	function handleClick(msg: Message) {
		if (msg.conversation_id) {
			openThread(msg.id, msg.conversation_id);
		}
	}
</script>

{#if messages.length === 0}
	<div class="text-center py-16 text-text-secondary">
		<svg class="w-10 h-10 mx-auto mb-3 text-border-active" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1">
			<path stroke-linecap="round" stroke-linejoin="round" d="M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4" />
		</svg>
		<p class="text-sm">No messages yet</p>
	</div>
{:else}
	<div>
		{#each messages as msg (msg.id)}
			<div
				class="group px-5 py-2.5 hover:bg-bg-tertiary/40 transition-colors cursor-pointer relative"
				role="button"
				tabindex="0"
				onmouseenter={() => (hoveredId = msg.id)}
				onmouseleave={() => (hoveredId = null)}
				onclick={() => handleClick(msg)}
				onkeydown={(e) => { if (e.key === 'Enter') handleClick(msg); }}
			>
				<div class="flex gap-3">
					<!-- Avatar -->
					<div class="w-9 h-9 rounded-lg {agentColor(msg.from_agent)} flex items-center justify-center text-sm font-bold text-white flex-shrink-0 mt-0.5">
						{msg.from_agent.charAt(0).toUpperCase()}
					</div>

					<!-- Content -->
					<div class="min-w-0 flex-1">
						<div class="flex items-center gap-2 mb-0.5">
							<span class="font-semibold text-sm text-text-primary hover:underline">{msg.from_agent}</span>
							{#if msg.to_agent}
								<svg class="w-3 h-3 text-text-secondary" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
									<path stroke-linecap="round" stroke-linejoin="round" d="M13 7l5 5m0 0l-5 5m5-5H6" />
								</svg>
								<span class="text-sm text-text-secondary">{msg.to_agent}</span>
							{/if}
							<span class="text-xs text-text-secondary">{formatTime(msg.created_at)}</span>
							{#if msg.status !== 'done'}
							<span class="{statusClass(msg.status)}">{msg.status}</span>
						{/if}
							{#if msg.priority >= 8}
								<span class="badge bg-accent-red/20 text-accent-red">P{msg.priority}</span>
							{:else if msg.priority >= 5}
								<span class="badge bg-accent-yellow/20 text-accent-yellow">P{msg.priority}</span>
							{/if}
						</div>
						<p class="text-sm text-text-primary/90 leading-relaxed whitespace-pre-wrap">{msg.body.length > 300 ? msg.body.slice(0, 300) + '...' : msg.body}</p>

						<!-- Thread link -->
						{#if showConversationLink && msg.conversation_id}
							<button
								class="mt-1 flex items-center gap-1 text-xs text-text-link hover:underline"
								onclick={(e) => { e.stopPropagation(); openThread(msg.id, msg.conversation_id); }}
							>
								<svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
									<path stroke-linecap="round" stroke-linejoin="round" d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
								</svg>
								{msg.reply_count ? `${msg.reply_count} replies` : 'View thread'}
							</button>
						{/if}
					</div>
				</div>

				<!-- Hover actions -->
				{#if hoveredId === msg.id}
					<div class="absolute top-1 right-4 flex items-center gap-0.5 bg-bg-secondary border border-border rounded-lg shadow-lg p-0.5">
						<button
							class="p-1.5 rounded hover:bg-bg-tertiary text-text-secondary hover:text-text-primary transition-colors"
							title="Reply in thread"
							onclick={(e) => { e.stopPropagation(); openThread(msg.id, msg.conversation_id); }}
						>
							<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
								<path stroke-linecap="round" stroke-linejoin="round" d="M3 10h10a8 8 0 018 8v2M3 10l6 6m-6-6l6-6" />
							</svg>
						</button>
						{#if msg.status === 'pending'}
							<button
								class="p-1.5 rounded hover:bg-bg-tertiary text-text-secondary hover:text-accent-green transition-colors"
								title="Mark done"
							>
								<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
									<path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
								</svg>
							</button>
						{/if}
					</div>
				{/if}
			</div>
		{/each}
	</div>
{/if}

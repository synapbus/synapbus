<script lang="ts">
	import { activeThread, closeThread } from '$lib/stores/thread';
	import { conversations as convsApi, messages as messagesApi } from '$lib/api/client';

	let conversation = $state<any>(null);
	let threadMessages = $state<any[]>([]);
	let loadingThread = $state(false);
	let replyBody = $state('');
	let sending = $state(false);
	let error = $state('');

	let currentThread = $state<{ messageId: number; conversationId: number } | null>(null);

	activeThread.subscribe((val) => {
		currentThread = val;
		if (val) {
			loadThread(val.conversationId);
		} else {
			conversation = null;
			threadMessages = [];
		}
	});

	async function loadThread(conversationId: number) {
		loadingThread = true;
		try {
			const res = await convsApi.get(conversationId);
			conversation = res.conversation;
			threadMessages = res.messages;
		} catch {
			// handled
		} finally {
			loadingThread = false;
		}
	}

	async function sendReply(e: SubmitEvent) {
		e.preventDefault();
		if (!replyBody.trim() || threadMessages.length === 0 || !currentThread) return;

		sending = true;
		error = '';
		try {
			const lastMsg = threadMessages[threadMessages.length - 1];
			await messagesApi.send({
				to: lastMsg.from_agent,
				body: replyBody.trim(),
				subject: conversation?.subject
			});
			replyBody = '';
			await loadThread(currentThread.conversationId);
		} catch (err: any) {
			error = err.message || 'Failed to send reply';
		} finally {
			sending = false;
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

	function statusClass(status: string): string {
		switch (status) {
			case 'pending': return 'badge-pending';
			case 'processing': return 'badge-processing';
			case 'done': return 'badge-done';
			case 'failed': return 'badge-failed';
			default: return 'badge bg-bg-tertiary text-text-secondary';
		}
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			const form = (e.target as HTMLElement).closest('form');
			if (form) form.requestSubmit();
		}
	}
</script>

{#if currentThread}
	<div class="w-[400px] h-full border-l border-border bg-bg-secondary flex flex-col flex-shrink-0">
		<!-- Thread header -->
		<div class="flex items-center justify-between h-12 px-4 border-b border-border flex-shrink-0">
			<div class="min-w-0">
				<h3 class="font-display font-bold text-sm text-text-primary truncate">Thread</h3>
				{#if conversation}
					<p class="text-[10px] text-text-secondary truncate">{conversation.subject || 'Conversation #' + conversation.id}</p>
				{/if}
			</div>
			<button
				class="p-1.5 rounded hover:bg-bg-tertiary text-text-secondary hover:text-text-primary transition-colors"
				onclick={closeThread}
				aria-label="Close thread"
			>
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
					<path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
				</svg>
			</button>
		</div>

		<!-- Thread messages -->
		<div class="flex-1 overflow-y-auto">
			{#if loadingThread}
				<div class="p-4 space-y-4">
					{#each Array(3) as _}
						<div class="flex gap-3">
							<div class="skeleton w-8 h-8 rounded-full flex-shrink-0"></div>
							<div class="flex-1 space-y-1.5">
								<div class="skeleton h-3 w-1/3"></div>
								<div class="skeleton h-3 w-2/3"></div>
							</div>
						</div>
					{/each}
				</div>
			{:else}
				{#each threadMessages as msg, i (msg.id)}
					<div class="px-4 py-3 hover:bg-bg-tertiary/50 transition-colors {i === 0 ? 'border-b border-border bg-bg-primary/30' : ''}">
						<div class="flex gap-2.5">
							<div class="w-7 h-7 rounded-full bg-bg-tertiary flex items-center justify-center text-[11px] font-bold text-text-secondary flex-shrink-0">
								{msg.from_agent.charAt(0).toUpperCase()}
							</div>
							<div class="min-w-0 flex-1">
								<div class="flex items-center gap-2 mb-0.5">
									<span class="font-semibold text-xs text-text-primary">{msg.from_agent}</span>
									<span class="text-[10px] text-text-secondary">{formatTime(msg.created_at)}</span>
									<span class="{statusClass(msg.status)} text-[10px]">{msg.status}</span>
								</div>
								<p class="text-xs text-text-primary/90 whitespace-pre-wrap leading-relaxed">{msg.body}</p>
							</div>
						</div>
					</div>
				{/each}
			{/if}
		</div>

		<!-- Reply input -->
		<div class="border-t border-border p-3 flex-shrink-0">
			{#if error}
				<div class="mb-2 px-2 py-1 bg-accent-red/10 rounded text-[11px] text-accent-red">{error}</div>
			{/if}
			<form onsubmit={sendReply}>
				<div class="bg-bg-input border border-border rounded-lg overflow-hidden focus-within:border-border-active">
					<textarea
						class="w-full px-3 py-2 bg-transparent text-xs text-text-primary placeholder-text-secondary resize-none outline-none"
						placeholder="Reply..."
						rows="2"
						bind:value={replyBody}
						onkeydown={handleKeydown}
					></textarea>
					<div class="flex items-center justify-end px-2 py-1.5">
						<button
							type="submit"
							class="px-3 py-1 bg-accent-green rounded text-xs font-medium text-white hover:brightness-110 transition-all disabled:opacity-50"
							disabled={sending || !replyBody.trim()}
						>
							{sending ? '...' : 'Send'}
						</button>
					</div>
				</div>
			</form>
		</div>
	</div>
{/if}

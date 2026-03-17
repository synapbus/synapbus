<script lang="ts">
	import { activeThread, closeThread } from '$lib/stores/thread';
	import { conversations as convsApi, messages as messagesApi } from '$lib/api/client';
	import MessageBody from '$lib/components/MessageBody.svelte';
	import AttachmentPreview from '$lib/components/AttachmentPreview.svelte';

	let conversation = $state<any>(null);
	let threadMessages = $state<any[]>([]);
	let loadingThread = $state(false);
	let replyBody = $state('');
	let sending = $state(false);
	let error = $state('');
	let loadError = $state('');

	let currentThread = $state<{ messageId: number; conversationId: number; fromAgent?: string } | null>(null);

	activeThread.subscribe((val) => {
		currentThread = val;
		loadError = '';
		error = '';
		replyBody = '';
		if (val) {
			loadThread(val.conversationId);
		} else {
			conversation = null;
			threadMessages = [];
		}
	});

	async function loadThread(conversationId: number) {
		loadingThread = true;
		loadError = '';
		try {
			const res = await convsApi.get(conversationId);
			conversation = res.conversation;
			threadMessages = res.messages;
		} catch (err: any) {
			loadError = err.message || 'Could not load thread';
		} finally {
			loadingThread = false;
		}
	}

	async function sendReply(e: SubmitEvent) {
		e.preventDefault();
		if (!replyBody.trim() || !currentThread) return;

		// Determine recipient: use last message's sender, or fallback to stored fromAgent
		const recipient = threadMessages.length > 0
			? threadMessages[threadMessages.length - 1].from_agent
			: currentThread.fromAgent;

		if (!recipient) {
			error = 'Cannot determine recipient';
			return;
		}

		sending = true;
		error = '';
		try {
			await messagesApi.send({
				to: recipient,
				body: replyBody.trim(),
				reply_to: currentThread.messageId,
				conversation_id: currentThread.conversationId,
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
				{:else if currentThread.fromAgent}
					<p class="text-[10px] text-text-secondary truncate">Reply to {currentThread.fromAgent}</p>
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
			{:else if loadError}
				<div class="p-4 text-center text-text-secondary text-xs">
					<p>Thread history unavailable</p>
					<p class="mt-1 text-[10px]">You can still send a reply below</p>
				</div>
			{:else if threadMessages.length === 0}
				<div class="p-4 text-center text-text-secondary text-xs">
					No messages in this thread yet
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
									{#if msg.status !== 'done'}
										<span class="{statusClass(msg.status)} text-[10px]">{msg.status}</span>
									{/if}
								</div>
								<div class="text-xs text-text-primary/90 leading-relaxed"><MessageBody body={msg.body} /></div>
								{#if msg.attachments && msg.attachments.length > 0}
									<div class="mt-1.5 flex flex-wrap gap-1.5">
										{#each msg.attachments as att (att.hash)}
											<AttachmentPreview attachment={att} />
										{/each}
									</div>
								{/if}
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

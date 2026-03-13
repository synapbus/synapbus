<script lang="ts">
	import { page } from '$app/stores';
	import { conversations as convsApi, messages as messagesApi } from '$lib/api/client';

	let conversation = $state<any>(null);
	let messagesList = $state<any[]>([]);
	let loadingData = $state(true);
	let replyBody = $state('');
	let sending = $state(false);
	let error = $state('');

	let convId = $derived(Number($page.params.id));

	async function loadConversation() {
		loadingData = true;
		try {
			const res = await convsApi.get(convId);
			conversation = res.conversation;
			messagesList = res.messages;
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
			loadConversation();
		}
	});

	async function sendReply(e: SubmitEvent) {
		e.preventDefault();
		if (!replyBody.trim() || messagesList.length === 0) return;

		sending = true;
		error = '';
		try {
			const lastMsg = messagesList[messagesList.length - 1];
			const to = lastMsg.from_agent;
			await messagesApi.send({
				to,
				body: replyBody.trim(),
				subject: conversation?.subject
			});
			replyBody = '';
			await loadConversation();
		} catch (err: any) {
			error = err.message || 'Failed to send reply';
		} finally {
			sending = false;
		}
	}

	function statusBadge(status: string): string {
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
		if (diff < 86400000) {
			return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
		}
		return d.toLocaleDateString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
	}

	function agentColor(name: string): string {
		const colors = ['bg-accent-blue', 'bg-accent-green', 'bg-accent-purple', 'bg-accent-yellow', 'bg-accent-red'];
		let hash = 0;
		for (let i = 0; i < name.length; i++) {
			hash = name.charCodeAt(i) + ((hash << 5) - hash);
		}
		return colors[Math.abs(hash) % colors.length];
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			const form = (e.target as HTMLElement).closest('form');
			if (form) form.requestSubmit();
		}
	}
</script>

<div class="flex flex-col h-full">
	{#if loadingData}
		<div class="p-5">
			<div class="skeleton h-6 w-1/3 mb-4"></div>
			<div class="space-y-4">
				{#each Array(3) as _}
					<div class="flex gap-3">
						<div class="skeleton w-9 h-9 rounded-lg flex-shrink-0"></div>
						<div class="flex-1 space-y-2">
							<div class="skeleton h-3 w-1/4"></div>
							<div class="skeleton h-3 w-3/4"></div>
						</div>
					</div>
				{/each}
			</div>
		</div>
	{:else if conversation}
		<!-- Header -->
		<div class="px-5 py-3 border-b border-border flex-shrink-0">
			<div class="flex items-center gap-3">
				<a href="/conversations" class="text-text-secondary hover:text-text-primary transition-colors" aria-label="Back to conversations">
					<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
						<path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
					</svg>
				</a>
				<div>
					<h1 class="font-semibold text-sm text-text-primary font-display">
						{conversation.subject || 'Conversation #' + conversation.id}
					</h1>
					<p class="text-[11px] text-text-secondary">
						Started by {conversation.created_by} -- {messagesList.length} messages
					</p>
				</div>
			</div>
		</div>

		<!-- Messages -->
		<div class="flex-1 overflow-y-auto">
			{#each messagesList as msg (msg.id)}
				<div class="px-5 py-3 hover:bg-bg-tertiary/30 transition-colors">
					<div class="flex gap-3">
						<div class="w-9 h-9 rounded-lg {agentColor(msg.from_agent)} flex items-center justify-center text-sm font-bold text-white flex-shrink-0">
							{msg.from_agent.charAt(0).toUpperCase()}
						</div>
						<div class="min-w-0 flex-1">
							<div class="flex items-center gap-2 mb-0.5">
								<span class="font-semibold text-sm text-text-primary">{msg.from_agent}</span>
								{#if msg.to_agent}
									<svg class="w-3 h-3 text-text-secondary" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
										<path stroke-linecap="round" stroke-linejoin="round" d="M13 7l5 5m0 0l-5 5m5-5H6" />
									</svg>
									<span class="text-xs text-text-secondary">{msg.to_agent}</span>
								{/if}
								<span class="{statusBadge(msg.status)}">{msg.status}</span>
								<span class="text-xs text-text-secondary ml-auto">{formatTime(msg.created_at)}</span>
							</div>
							<div class="text-sm text-text-primary/90 whitespace-pre-wrap leading-relaxed">{msg.body}</div>
						</div>
					</div>
				</div>
			{/each}
		</div>

		<!-- Reply form -->
		<div class="border-t border-border p-4 flex-shrink-0">
			{#if error}
				<div class="mb-2 px-3 py-1.5 bg-accent-red/10 rounded text-xs text-accent-red">{error}</div>
			{/if}
			<form onsubmit={sendReply}>
				<div class="bg-bg-input border border-border rounded-lg overflow-hidden focus-within:border-border-active">
					<textarea
						class="w-full px-4 py-3 bg-transparent text-sm text-text-primary placeholder-text-secondary resize-none outline-none"
						placeholder="Type a reply..."
						rows="2"
						bind:value={replyBody}
						onkeydown={handleKeydown}
					></textarea>
					<div class="flex items-center justify-end px-3 py-2">
						<button
							type="submit"
							class="px-4 py-1.5 bg-accent-green rounded text-xs font-semibold text-white hover:brightness-110 transition-all disabled:opacity-50 flex items-center gap-1.5"
							disabled={sending || !replyBody.trim()}
						>
							{#if sending}
								Sending...
							{:else}
								<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
									<path stroke-linecap="round" stroke-linejoin="round" d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" />
								</svg>
								Send
							{/if}
						</button>
					</div>
				</div>
			</form>
		</div>
	{:else}
		<div class="p-8 text-center text-text-secondary text-sm">
			Conversation not found
		</div>
	{/if}
</div>

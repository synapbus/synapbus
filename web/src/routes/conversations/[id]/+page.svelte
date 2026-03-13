<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { conversations as convsApi, messages as messagesApi } from '$lib/api/client';

	let conversation = $state<any>(null);
	let messagesList = $state<any[]>([]);
	let loadingData = $state(true);
	let replyBody = $state('');
	let sending = $state(false);
	let error = $state('');

	$: convId = Number($page.params.id);

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

	onMount(loadConversation);

	async function sendReply(e: SubmitEvent) {
		e.preventDefault();
		if (!replyBody.trim() || messagesList.length === 0) return;

		sending = true;
		error = '';
		try {
			// Reply to the most recent sender
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
			default: return 'badge bg-gray-100 text-gray-800';
		}
	}
</script>

<div class="max-w-3xl mx-auto">
	<a href="/conversations" class="text-sm text-primary-600 hover:text-primary-700 dark:text-primary-400 mb-4 inline-block">
		&larr; Back to conversations
	</a>

	{#if loadingData}
		<div class="card p-6">
			<div class="skeleton h-6 w-1/3 mb-4"></div>
			<div class="space-y-3">
				{#each Array(3) as _}
					<div class="skeleton h-16 w-full"></div>
				{/each}
			</div>
		</div>
	{:else if conversation}
		<div class="card">
			<div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
				<h1 class="font-semibold text-lg text-gray-900 dark:text-gray-100">
					{conversation.subject || 'Conversation #' + conversation.id}
				</h1>
				<p class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
					Started by {conversation.created_by} &middot; {messagesList.length} messages
				</p>
			</div>

			<!-- Messages thread -->
			<div class="divide-y divide-gray-100 dark:divide-gray-700 max-h-[60vh] overflow-y-auto">
				{#each messagesList as msg (msg.id)}
					<div class="px-4 py-3">
						<div class="flex items-center gap-2 mb-1">
							<div class="w-6 h-6 rounded-full bg-primary-100 dark:bg-primary-900 flex items-center justify-center text-xs font-medium text-primary-700 dark:text-primary-300">
								{msg.from_agent.charAt(0).toUpperCase()}
							</div>
							<span class="font-medium text-sm text-gray-900 dark:text-gray-100">{msg.from_agent}</span>
							{#if msg.to_agent}
								<span class="text-xs text-gray-400">&rarr; {msg.to_agent}</span>
							{/if}
							<span class="{statusBadge(msg.status)}">{msg.status}</span>
							<span class="text-xs text-gray-500 dark:text-gray-400 ml-auto">
								{new Date(msg.created_at).toLocaleString()}
							</span>
						</div>
						<div class="ml-8 text-sm text-gray-700 dark:text-gray-300 whitespace-pre-wrap">{msg.body}</div>
					</div>
				{/each}
			</div>

			<!-- Reply form -->
			<div class="border-t border-gray-200 dark:border-gray-700 p-4">
				{#if error}
					<div class="mb-3 p-2 bg-red-50 dark:bg-red-900/30 rounded text-sm text-red-700 dark:text-red-300">{error}</div>
				{/if}
				<form class="flex gap-2" onsubmit={sendReply}>
					<input type="text" class="input flex-1" placeholder="Type a reply..." bind:value={replyBody} />
					<button type="submit" class="btn-primary" disabled={sending || !replyBody.trim()}>
						{sending ? '...' : 'Send'}
					</button>
				</form>
			</div>
		</div>
	{:else}
		<div class="card p-8 text-center">
			<p class="text-gray-500 dark:text-gray-400">Conversation not found</p>
		</div>
	{/if}
</div>

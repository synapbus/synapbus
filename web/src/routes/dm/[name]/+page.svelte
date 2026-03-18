<script lang="ts">
	import { page } from '$app/stores';
	import { agents as agentsApi, messages as messagesApi } from '$lib/api/client';
	import { openThread, closeThread } from '$lib/stores/thread';
	import { notifications } from '$lib/stores/notifications';
	import MessageBody from '$lib/components/MessageBody.svelte';
	import WorkflowBadge from '$lib/components/WorkflowBadge.svelte';
	import ReactionPills from '$lib/components/ReactionPills.svelte';

	let peerAgent = $derived($page.params.name);
	let peer = $state<any>(null);
	let messageList = $state<any[]>([]);
	let ownAgents = $state<any[]>([]);
	let loadingData = $state(true);
	let lastReadMessageId = $state<number | null>(null);

	// Compose state
	let body = $state('');
	let sending = $state(false);
	let sendError = $state('');

	// Mark-as-read timer
	let markReadTimer: ReturnType<typeof setTimeout> | null = null;

	let messagesContainer: HTMLDivElement;

	async function loadData() {
		loadingData = true;
		try {
			const [agRes, peerRes] = await Promise.all([
				agentsApi.list(),
				agentsApi.get(peerAgent).catch(() => null)
			]);
			ownAgents = agRes.agents ?? [];
			peer = peerRes?.agent ?? { name: peerAgent };
			await loadMessages();
		} catch {
			// handled
		} finally {
			loadingData = false;
		}
	}

	async function loadMessages() {
		try {
			const res = await agentsApi.messages(peerAgent) as any;
			messageList = res.messages;
			lastReadMessageId = res.last_read_message_id ?? null;
			scrollToBottom();
			startMarkReadTimer();
		} catch {
			// handled
		}
	}

	function startMarkReadTimer() {
		clearMarkReadTimer();
		if (lastReadMessageId !== null && messageList.length > 0) {
			const lastMsgId = messageList[messageList.length - 1]?.id;
			if (lastMsgId > lastReadMessageId) {
				markReadTimer = setTimeout(() => {
					notifications.markAsRead('dm', peerAgent, lastMsgId);
				}, 2000);
			}
		}
	}

	function clearMarkReadTimer() {
		if (markReadTimer !== null) {
			clearTimeout(markReadTimer);
			markReadTimer = null;
		}
	}

	$effect(() => {
		return () => clearMarkReadTimer();
	});

	function scrollToBottom() {
		requestAnimationFrame(() => {
			if (messagesContainer) {
				messagesContainer.scrollTop = messagesContainer.scrollHeight;
			}
		});
	}

	let _prevPeer = $state('');
	$effect(() => {
		if (peerAgent !== _prevPeer) {
			clearMarkReadTimer();
			_prevPeer = peerAgent;
			lastReadMessageId = null;
			closeThread();
			loadData();
		}
	});

	async function handleSend() {
		if (!body.trim()) return;
		sending = true;
		sendError = '';
		try {
			await messagesApi.send({
				to: peerAgent,
				body: body.trim()
			});
			body = '';
			await loadMessages();
		} catch (err: any) {
			sendError = err.message || 'Failed to send message';
		} finally {
			sending = false;
		}
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			handleSend();
		}
	}

	function agentType(name: string): string | null {
		// Check own agents first
		const ownAgent = ownAgents.find(a => a.name === name);
		if (ownAgent) return ownAgent.type;
		// Check peer agent
		if (peer && peer.name === name && peer.type) return peer.type;
		return null;
	}

	function agentColor(name: string): string {
		const colors = ['bg-accent-blue', 'bg-accent-green', 'bg-accent-purple', 'bg-accent-yellow', 'bg-accent-red'];
		let hash = 0;
		for (let i = 0; i < name.length; i++) {
			hash = name.charCodeAt(i) + ((hash << 5) - hash);
		}
		return colors[Math.abs(hash) % colors.length];
	}

	function formatTime(iso: string): string {
		const d = new Date(iso);
		const now = new Date();
		const diff = now.getTime() - d.getTime();
		if (diff < 60000) return 'just now';
		if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
		if (diff < 86400000) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
		return d.toLocaleDateString([], { month: 'short', day: 'numeric' }) + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
	}

	let isOwnAgent = $derived(ownAgents.some(a => a.name === peerAgent));
</script>

<div class="flex flex-col flex-1 min-h-0 overflow-hidden">
	<!-- DM header -->
	<div class="flex items-center gap-3 px-5 py-3 border-b border-border flex-shrink-0 bg-bg-secondary">
		<div class="relative">
			<div class="w-8 h-8 rounded-full {agentColor(peerAgent)} flex items-center justify-center text-sm font-bold text-white">
				{(peer?.display_name || peerAgent).charAt(0).toUpperCase()}
			</div>
			{#if peer?.status === 'active'}
				<span class="absolute -bottom-0.5 -right-0.5 w-2.5 h-2.5 rounded-full bg-accent-green border-2 border-bg-secondary"></span>
			{/if}
		</div>
		<div class="min-w-0">
			<h2 class="text-sm font-bold text-text-primary font-display truncate">{peer?.display_name || peerAgent}</h2>
			<p class="text-[10px] text-text-secondary font-mono">@{peerAgent}</p>
		</div>
		{#if peer?.type === 'ai'}
			<span class="text-[9px] font-mono text-accent-purple bg-accent-purple/10 px-1.5 py-0.5 rounded">AI</span>
		{/if}
		<div class="ml-auto">
			<a
				href="/agents/{peerAgent}"
				class="p-1.5 rounded hover:bg-bg-tertiary text-text-secondary hover:text-text-primary transition-colors"
				title="Agent details"
			>
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
					<path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
				</svg>
			</a>
		</div>
	</div>

	<!-- Messages -->
	{#if loadingData}
		<div class="flex-1 p-5 space-y-4">
			{#each Array(5) as _}
				<div class="flex gap-3">
					<div class="skeleton w-9 h-9 rounded-lg flex-shrink-0"></div>
					<div class="flex-1 space-y-2">
						<div class="skeleton h-3 w-1/4"></div>
						<div class="skeleton h-3 w-2/3"></div>
					</div>
				</div>
			{/each}
		</div>
	{:else}
		<div class="flex-1 overflow-y-auto" bind:this={messagesContainer}>
			{#if messageList.length === 0}
				<div class="flex flex-col items-center justify-center h-full text-center p-5">
					<div class="w-12 h-12 rounded-full {agentColor(peerAgent)} flex items-center justify-center text-xl font-bold text-white mb-3">
						{(peer?.display_name || peerAgent).charAt(0).toUpperCase()}
					</div>
					<h3 class="text-base font-semibold text-text-primary font-display mb-1">
						{peer?.display_name || peerAgent}
					</h3>
					<p class="text-sm text-text-secondary">
						{#if isOwnAgent}
							This is your own agent. Send a test message!
						{:else}
							Start a conversation with this agent.
						{/if}
					</p>
				</div>
			{:else}
				<div class="py-2">
					{#each messageList as msg, i (msg.id)}
						{#if lastReadMessageId !== null && msg.id > lastReadMessageId && (i === 0 || messageList[i - 1].id <= lastReadMessageId)}
							<div class="flex items-center gap-3 px-5 py-1 my-1">
								<div class="flex-1 h-px bg-accent-red/50"></div>
								<span class="text-[11px] font-medium text-accent-red flex-shrink-0">New messages</span>
								<div class="flex-1 h-px bg-accent-red/50"></div>
							</div>
						{/if}
						<div class="group px-5 py-2 hover:bg-bg-tertiary/40 transition-colors relative">
							<div class="flex gap-3">
								<div class="w-9 h-9 rounded-lg {agentColor(msg.from_agent)} flex items-center justify-center text-sm font-bold text-white flex-shrink-0 mt-0.5">
									{msg.from_agent.charAt(0).toUpperCase()}
								</div>
								<div class="min-w-0 flex-1">
									<div class="flex items-center gap-2 mb-0.5">
										<span class="font-semibold text-sm text-text-primary">{msg.from_agent}</span>
										{#if agentType(msg.from_agent) === 'ai'}
											<span class="text-[9px] font-mono text-accent-purple bg-accent-purple/10 px-1 rounded">AI</span>
										{:else if agentType(msg.from_agent) === 'human'}
											<span class="text-[9px] font-mono text-accent-blue bg-accent-blue/10 px-1 rounded">Human</span>
										{/if}
										{#if msg.to_agent}
											<svg class="w-3 h-3 text-text-secondary" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
												<path stroke-linecap="round" stroke-linejoin="round" d="M13 7l5 5m0 0l-5 5m5-5H6" />
											</svg>
											<span class="text-xs text-text-secondary">{msg.to_agent}</span>
										{/if}
										<span class="text-xs text-text-secondary">{formatTime(msg.created_at)}</span>
										{#if msg.status !== 'done'}
											<span class="text-[9px] px-1.5 py-0.5 rounded {msg.status === 'pending' ? 'bg-accent-yellow/20 text-accent-yellow' : msg.status === 'processing' ? 'bg-accent-blue/20 text-accent-blue' : 'bg-bg-tertiary text-text-secondary'}">{msg.status}</span>
										{/if}
									</div>
									<div class="text-sm text-text-primary/90 leading-relaxed"><MessageBody body={msg.body} /></div>
									{#if msg.workflow_state}
										<WorkflowBadge state={msg.workflow_state} />
									{/if}
									<ReactionPills reactions={msg.reactions ?? []} messageId={msg.id} />
									{#if msg.reply_count > 0}
										<button
											class="mt-1 flex items-center gap-1 text-xs text-accent-blue hover:underline"
											onclick={() => openThread(msg.id, msg.conversation_id, msg.from_agent)}
										>
											<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
												<path stroke-linecap="round" stroke-linejoin="round" d="M7.5 8.25h9m-9 3H12m-9.75 1.51c0 1.6 1.123 2.994 2.707 3.227 1.087.16 2.185.283 3.293.369V21l4.076-4.076a1.526 1.526 0 011.037-.443 48.282 48.282 0 005.68-.494c1.584-.233 2.707-1.626 2.707-3.228V6.741c0-1.602-1.123-2.995-2.707-3.228A48.394 48.394 0 0012 3c-2.392 0-4.744.175-7.043.513C3.373 3.746 2.25 5.14 2.25 6.741v6.018z" />
											</svg>
											{msg.reply_count} {msg.reply_count === 1 ? 'reply' : 'replies'}
										</button>
									{/if}
								</div>
								<!-- Reply button (visible on hover) -->
								<button
									class="absolute top-1.5 right-3 p-1 rounded hover:bg-bg-tertiary text-text-secondary hover:text-text-primary opacity-0 group-hover:opacity-100 transition-opacity"
									title="Reply in thread"
									onclick={() => openThread(msg.id, msg.conversation_id, msg.from_agent)}
								>
									<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
										<path stroke-linecap="round" stroke-linejoin="round" d="M7.5 8.25h9m-9 3H12m-9.75 1.51c0 1.6 1.123 2.994 2.707 3.227 1.087.16 2.185.283 3.293.369V21l4.076-4.076a1.526 1.526 0 011.037-.443 48.282 48.282 0 005.68-.494c1.584-.233 2.707-1.626 2.707-3.228V6.741c0-1.602-1.123-2.995-2.707-3.228A48.394 48.394 0 0012 3c-2.392 0-4.744.175-7.043.513C3.373 3.746 2.25 5.14 2.25 6.741v6.018z" />
									</svg>
								</button>
							</div>
						</div>
					{/each}
				</div>
			{/if}
		</div>

		<!-- Compose bar -->
		<div class="px-4 pb-4 pt-2 flex-shrink-0">
			{#if sendError}
				<div class="mb-2 px-3 py-1.5 bg-accent-red/10 rounded text-xs text-accent-red">{sendError}</div>
			{/if}
			{#if ownAgents.length === 0}
				<div class="px-3 py-2 bg-bg-tertiary rounded text-xs text-text-secondary text-center">
					Register an agent to send messages
				</div>
			{:else}
				<div class="flex items-end gap-2 bg-bg-tertiary rounded-lg border border-border focus-within:border-border-active transition-colors">
					<textarea
						placeholder="Message {peer?.display_name || peerAgent}..."
						class="flex-1 px-3 py-2.5 bg-transparent text-sm text-text-primary placeholder-text-secondary resize-none outline-none min-h-[40px] max-h-[120px]"
						bind:value={body}
						rows="1"
						onkeydown={handleKeydown}
					></textarea>
					<button
						class="p-2 mr-1 mb-1 rounded-md bg-accent-green text-white hover:brightness-110 transition-all disabled:opacity-40 disabled:cursor-not-allowed"
						disabled={sending || !body.trim()}
						onclick={handleSend}
					>
						<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
							<path stroke-linecap="round" stroke-linejoin="round" d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" />
						</svg>
					</button>
				</div>
			{/if}
		</div>
	{/if}
</div>

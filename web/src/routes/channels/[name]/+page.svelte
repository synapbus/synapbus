<script lang="ts">
	import { page } from '$app/stores';
	import { channels as channelsApi, messages as messagesApi, agents as agentsApi } from '$lib/api/client';

	let channel = $state<any>(null);
	let members = $state<any[]>([]);
	let messageList = $state<any[]>([]);
	let agentList = $state<any[]>([]);
	let loadingData = $state(true);
	let joinError = $state('');
	let joining = $state(false);
	let showInfo = $state(false);

	// Compose state
	let body = $state('');
	let sending = $state(false);
	let sendError = $state('');

	let channelName = $derived($page.params.name);

	let messagesContainer: HTMLDivElement;

	async function loadChannel() {
		loadingData = true;
		try {
			const [chRes, agRes] = await Promise.all([
				channelsApi.get(channelName),
				agentsApi.list()
			]);
			channel = chRes.channel;
			members = chRes.members;
			agentList = agRes.agents ?? [];
			await loadMessages();
		} catch {
			// handled
		} finally {
			loadingData = false;
		}
	}

	async function loadMessages() {
		if (!channel) return;
		try {
			const res = await channelsApi.messages(channelName);
			messageList = res.messages;
			scrollToBottom();
		} catch {
			// handled
		}
	}

	function scrollToBottom() {
		requestAnimationFrame(() => {
			if (messagesContainer) {
				messagesContainer.scrollTop = messagesContainer.scrollHeight;
			}
		});
	}

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			loadChannel();
		}
	});

	async function handleJoin() {
		joining = true;
		joinError = '';
		try {
			await channelsApi.join(channelName);
			await loadChannel();
		} catch (err: any) {
			joinError = err.message || 'Failed to join channel';
		} finally {
			joining = false;
		}
	}

	async function handleSend() {
		if (!body.trim() || !agentList.length) return;
		sending = true;
		sendError = '';
		try {
			await messagesApi.send({
				from: agentList[0].name,
				body: body.trim(),
				channel_id: channel.id
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

	let isMember = $derived(
		members.some(m => agentList.some(a => a.name === m.agent_name))
	);
</script>

<div class="flex flex-col flex-1 min-h-0 overflow-hidden">
	<!-- Channel header -->
	<div class="flex items-center justify-between px-5 py-3 border-b border-border flex-shrink-0 bg-bg-secondary">
		<div class="flex items-center gap-2 min-w-0">
			<span class="text-lg font-bold text-text-primary font-display font-mono">#{channelName}</span>
			{#if channel?.description}
				<span class="text-xs text-text-secondary truncate hidden sm:inline">— {channel.description}</span>
			{/if}
		</div>
		<div class="flex items-center gap-2 flex-shrink-0">
			{#if !isMember}
				<button class="btn-primary text-xs" onclick={handleJoin} disabled={joining}>
					{joining ? 'Joining...' : 'Join Channel'}
				</button>
			{/if}
			<button
				class="p-1.5 rounded hover:bg-bg-tertiary text-text-secondary hover:text-text-primary transition-colors"
				title="Channel info"
				onclick={() => (showInfo = !showInfo)}
			>
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
					<path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
				</svg>
			</button>
		</div>
	</div>

	{#if joinError}
		<div class="px-5 py-2 bg-accent-red/10 text-xs text-accent-red border-b border-accent-red/20">{joinError}</div>
	{/if}

	<div class="flex flex-1 min-h-0">
		<!-- Messages area -->
		<div class="flex flex-col flex-1 min-w-0">
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
				<!-- Message list -->
				<div class="flex-1 overflow-y-auto" bind:this={messagesContainer}>
					{#if messageList.length === 0}
						<div class="flex flex-col items-center justify-center h-full text-center p-5">
							<div class="w-12 h-12 rounded-2xl bg-accent-blue/10 flex items-center justify-center mb-3">
								<span class="text-2xl font-bold text-accent-blue font-mono">#</span>
							</div>
							<h3 class="text-base font-semibold text-text-primary font-display mb-1">Welcome to #{channelName}</h3>
							<p class="text-sm text-text-secondary">
								{channel?.description || 'This is the start of the channel.'}
							</p>
						</div>
					{:else}
						<div class="py-2">
							{#each messageList as msg (msg.id)}
								<div class="group px-5 py-2 hover:bg-bg-tertiary/40 transition-colors">
									<div class="flex gap-3">
										<div class="w-9 h-9 rounded-lg {agentColor(msg.from_agent)} flex items-center justify-center text-sm font-bold text-white flex-shrink-0 mt-0.5">
											{msg.from_agent.charAt(0).toUpperCase()}
										</div>
										<div class="min-w-0 flex-1">
											<div class="flex items-center gap-2 mb-0.5">
												<span class="font-semibold text-sm text-text-primary">{msg.from_agent}</span>
												<span class="text-xs text-text-secondary">{formatTime(msg.created_at)}</span>
											</div>
											<p class="text-sm text-text-primary/90 leading-relaxed whitespace-pre-wrap">{msg.body}</p>
										</div>
									</div>
								</div>
							{/each}
						</div>
					{/if}
				</div>

				<!-- Compose bar -->
				{#if isMember || !channel?.is_private}
					<div class="px-4 pb-4 pt-2 flex-shrink-0">
						{#if sendError}
							<div class="mb-2 px-3 py-1.5 bg-accent-red/10 rounded text-xs text-accent-red">{sendError}</div>
						{/if}
						{#if agentList.length === 0}
							<div class="px-3 py-2 bg-bg-tertiary rounded text-xs text-text-secondary text-center">
								Register an agent to send messages
							</div>
						{:else}
							<div class="flex items-end gap-2 bg-bg-tertiary rounded-lg border border-border focus-within:border-border-active transition-colors">
								<textarea
									placeholder="Message #{channelName}..."
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
							<p class="text-[10px] text-text-secondary mt-1 px-1">Sending as <span class="font-mono">{agentList[0]?.name}</span></p>
						{/if}
					</div>
				{/if}
			{/if}
		</div>

		<!-- Info panel (collapsible) -->
		{#if showInfo}
			<div class="w-64 border-l border-border bg-bg-secondary flex-shrink-0 overflow-y-auto">
				<div class="p-4">
					<div class="flex items-center justify-between mb-3">
						<h3 class="font-semibold text-sm text-text-primary font-display">Channel Info</h3>
						<button class="text-text-secondary hover:text-text-primary" onclick={() => (showInfo = false)}>
							<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
								<path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
							</svg>
						</button>
					</div>
					{#if channel}
						{#if channel.description}
							<div class="mb-3">
								<p class="text-xs text-text-secondary mb-0.5">Description</p>
								<p class="text-sm text-text-primary">{channel.description}</p>
							</div>
						{/if}
						{#if channel.topic}
							<div class="mb-3">
								<p class="text-xs text-text-secondary mb-0.5">Topic</p>
								<p class="text-sm text-text-primary">{channel.topic}</p>
							</div>
						{/if}
						<div class="mb-3">
							<p class="text-xs text-text-secondary mb-0.5">Type</p>
							<span class="badge bg-accent-blue/20 text-accent-blue">{channel.type}</span>
						</div>
					{/if}

					<div class="border-t border-border pt-3 mt-3">
						<h4 class="text-xs font-medium text-text-secondary mb-2">Members ({members.length})</h4>
						{#if members.length === 0}
							<p class="text-xs text-text-secondary italic">No members</p>
						{:else}
							<div class="space-y-1.5">
								{#each members as member}
									<div class="flex items-center gap-2">
										<span class="w-5 h-5 rounded-full bg-bg-tertiary flex items-center justify-center text-[10px] font-bold text-text-secondary">
											{member.agent_name.charAt(0).toUpperCase()}
										</span>
										<span class="text-xs text-text-primary truncate">{member.agent_name}</span>
										{#if member.role === 'owner'}
											<span class="text-[9px] text-accent-yellow ml-auto">owner</span>
										{/if}
									</div>
								{/each}
							</div>
						{/if}
					</div>

					<div class="border-t border-border pt-3 mt-3 space-y-2">
						{#if !isMember}
							<button class="btn-primary text-xs w-full" onclick={handleJoin} disabled={joining}>
								{joining ? 'Joining...' : 'Join Channel'}
							</button>
						{:else}
							<button class="btn-secondary text-xs w-full" onclick={async () => { await channelsApi.leave(channelName); await loadChannel(); }}>
								Leave Channel
							</button>
						{/if}
					</div>
				</div>
			</div>
		{/if}
	</div>
</div>

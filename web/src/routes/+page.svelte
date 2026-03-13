<script lang="ts">
	import { messages as messagesApi, conversations as convsApi, agents as agentsApi } from '$lib/api/client';
	import MessageList from '$lib/components/MessageList.svelte';
	import ComposeForm from '$lib/components/ComposeForm.svelte';

	let recentMessages = $state<any[]>([]);
	let recentConversations = $state<any[]>([]);
	let agentCount = $state(0);
	let loadingData = $state(true);
	let showCompose = $state(false);

	async function loadData() {
		loadingData = true;
		try {
			const [msgRes, convRes, agentRes] = await Promise.all([
				messagesApi.list({ limit: 20 }),
				convsApi.list(),
				agentsApi.list()
			]);
			recentMessages = msgRes.messages;
			recentConversations = convRes.conversations;
			agentCount = agentRes.agents.length;
		} catch {
			// Errors handled by API client
		} finally {
			loadingData = false;
		}
	}

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			loadData();
		}
	});
</script>

<div class="p-5 max-w-5xl">
	<!-- Top bar -->
	<div class="flex items-center justify-between mb-5">
		<div>
			<h1 class="text-xl font-bold text-text-primary font-display">Dashboard</h1>
			<p class="text-xs text-text-secondary mt-0.5">Mission control overview</p>
		</div>
		<button
			class="btn-primary flex items-center gap-1.5"
			onclick={() => (showCompose = !showCompose)}
		>
			{#if showCompose}
				Cancel
			{:else}
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
					<path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
				</svg>
				New Message
			{/if}
		</button>
	</div>

	{#if showCompose}
		<div class="mb-5">
			<ComposeForm onSent={() => { showCompose = false; loadData(); }} />
		</div>
	{/if}

	<!-- Stats -->
	<div class="grid grid-cols-3 gap-3 mb-5">
		<div class="card p-4">
			<p class="text-xs text-text-secondary mb-1">Messages</p>
			<p class="text-2xl font-bold text-text-primary font-display">{loadingData ? '-' : recentMessages.length}</p>
		</div>
		<div class="card p-4">
			<p class="text-xs text-text-secondary mb-1">Conversations</p>
			<p class="text-2xl font-bold text-text-primary font-display">{loadingData ? '-' : recentConversations.length}</p>
		</div>
		<div class="card p-4">
			<p class="text-xs text-text-secondary mb-1">Agents</p>
			<p class="text-2xl font-bold text-text-primary font-display">{loadingData ? '-' : agentCount}</p>
		</div>
	</div>

	<!-- Recent Conversations -->
	{#if recentConversations.length > 0}
		<div class="card mb-5">
			<div class="px-5 py-3 border-b border-border">
				<h2 class="font-semibold text-sm text-text-primary font-display">Recent Conversations</h2>
			</div>
			<div class="divide-y divide-border">
				{#each recentConversations.slice(0, 5) as conv}
					<a href="/conversations/{conv.id}" class="block px-5 py-3 hover:bg-bg-tertiary/50 transition-colors">
						<div class="flex items-center justify-between">
							<div class="min-w-0">
								<p class="font-medium text-sm text-text-primary truncate">
									{conv.subject || 'Untitled conversation'}
								</p>
								<p class="text-xs text-text-secondary truncate mt-0.5">
									{conv.last_agent}: {conv.last_message}
								</p>
							</div>
							<div class="flex items-center gap-2 flex-shrink-0 ml-3">
								<span class="badge bg-bg-tertiary text-text-secondary">{conv.message_count} msgs</span>
							</div>
						</div>
					</a>
				{/each}
			</div>
			{#if recentConversations.length > 5}
				<div class="px-5 py-2.5 border-t border-border">
					<a href="/conversations" class="text-xs text-text-link hover:underline">View all conversations</a>
				</div>
			{/if}
		</div>
	{/if}

	<!-- Recent Messages -->
	<div class="card">
		<div class="px-5 py-3 border-b border-border">
			<h2 class="font-semibold text-sm text-text-primary font-display">Recent Messages</h2>
		</div>
		{#if loadingData}
			<div class="p-5 space-y-4">
				{#each Array(3) as _}
					<div class="flex gap-3">
						<div class="skeleton w-9 h-9 rounded-lg flex-shrink-0"></div>
						<div class="flex-1 space-y-2">
							<div class="skeleton h-3 w-1/3"></div>
							<div class="skeleton h-3 w-2/3"></div>
						</div>
					</div>
				{/each}
			</div>
		{:else}
			<MessageList messages={recentMessages} showConversationLink={true} />
		{/if}
	</div>

	{#if !loadingData && recentMessages.length === 0 && agentCount === 0}
		<div class="card p-10 text-center mt-5">
			<div class="w-12 h-12 mx-auto rounded-2xl bg-accent-purple/20 flex items-center justify-center mb-4">
				<svg class="w-6 h-6 text-accent-purple" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
					<path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" />
				</svg>
			</div>
			<h3 class="text-base font-semibold text-text-primary font-display mb-2">Welcome to SynapBus</h3>
			<p class="text-sm text-text-secondary mb-4">No conversations yet. Register an agent to get started.</p>
			<a href="/agents" class="btn-primary inline-block">Register an Agent</a>
		</div>
	{/if}
</div>

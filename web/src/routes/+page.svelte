<script lang="ts">
	import { onMount } from 'svelte';
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

	onMount(loadData);
</script>

<div class="max-w-4xl mx-auto">
	<div class="flex items-center justify-between mb-6">
		<h1 class="text-2xl font-bold text-gray-900 dark:text-white">Dashboard</h1>
		<button class="btn-primary" onclick={() => (showCompose = !showCompose)}>
			{showCompose ? 'Cancel' : 'New Message'}
		</button>
	</div>

	{#if showCompose}
		<div class="mb-6">
			<ComposeForm onSent={() => { showCompose = false; loadData(); }} />
		</div>
	{/if}

	<!-- Stats -->
	<div class="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-6">
		<div class="card p-4">
			<p class="text-sm text-gray-500 dark:text-gray-400">Messages</p>
			<p class="text-2xl font-bold text-gray-900 dark:text-white">{loadingData ? '-' : recentMessages.length}</p>
		</div>
		<div class="card p-4">
			<p class="text-sm text-gray-500 dark:text-gray-400">Conversations</p>
			<p class="text-2xl font-bold text-gray-900 dark:text-white">{loadingData ? '-' : recentConversations.length}</p>
		</div>
		<div class="card p-4">
			<p class="text-sm text-gray-500 dark:text-gray-400">Agents</p>
			<p class="text-2xl font-bold text-gray-900 dark:text-white">{loadingData ? '-' : agentCount}</p>
		</div>
	</div>

	<!-- Recent Conversations -->
	{#if recentConversations.length > 0}
		<div class="card mb-6">
			<div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
				<h2 class="font-semibold text-gray-900 dark:text-gray-100">Recent Conversations</h2>
			</div>
			<div class="divide-y divide-gray-100 dark:divide-gray-700">
				{#each recentConversations.slice(0, 5) as conv}
					<a href="/conversations/{conv.id}" class="block px-4 py-3 hover:bg-gray-50 dark:hover:bg-gray-700/50 transition-colors">
						<div class="flex items-center justify-between">
							<div class="min-w-0">
								<p class="font-medium text-sm text-gray-900 dark:text-gray-100 truncate">
									{conv.subject || 'Untitled conversation'}
								</p>
								<p class="text-xs text-gray-500 dark:text-gray-400 truncate mt-0.5">
									{conv.last_agent}: {conv.last_message}
								</p>
							</div>
							<div class="flex items-center gap-2 flex-shrink-0 ml-3">
								<span class="badge bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-400">{conv.message_count} msgs</span>
							</div>
						</div>
					</a>
				{/each}
			</div>
			{#if recentConversations.length > 5}
				<div class="px-4 py-2 border-t border-gray-200 dark:border-gray-700">
					<a href="/conversations" class="text-sm text-primary-600 hover:text-primary-700 dark:text-primary-400">View all conversations</a>
				</div>
			{/if}
		</div>
	{/if}

	<!-- Recent Messages -->
	<div class="card">
		<div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
			<h2 class="font-semibold text-gray-900 dark:text-gray-100">Recent Messages</h2>
		</div>
		{#if loadingData}
			<div class="p-4 space-y-3">
				{#each Array(3) as _}
					<div class="space-y-2">
						<div class="skeleton h-4 w-1/3"></div>
						<div class="skeleton h-3 w-2/3"></div>
					</div>
				{/each}
			</div>
		{:else}
			<MessageList messages={recentMessages} showConversationLink={true} />
		{/if}
	</div>

	{#if !loadingData && recentMessages.length === 0 && agentCount === 0}
		<div class="card p-8 text-center mt-6">
			<h3 class="text-lg font-medium text-gray-900 dark:text-gray-100 mb-2">Welcome to SynapBus</h3>
			<p class="text-gray-500 dark:text-gray-400 mb-4">No conversations yet. Register an agent to get started.</p>
			<a href="/agents" class="btn-primary inline-block">Register an Agent</a>
		</div>
	{/if}
</div>

<script lang="ts">
	import { analytics, messages as messagesApi, conversations as convsApi, agents as agentsApi } from '$lib/api/client';
	import MessageList from '$lib/components/MessageList.svelte';
	import ComposeForm from '$lib/components/ComposeForm.svelte';
	import AnalyticsChart from '$lib/components/AnalyticsChart.svelte';
	import TopList from '$lib/components/TopList.svelte';

	let recentMessages = $state<any[]>([]);
	let recentConversations = $state<any[]>([]);
	let agentTypeMap = $state<Record<string, string>>({});
	let loadingData = $state(true);
	let showCompose = $state(false);

	// Analytics state
	let span = $state('24h');
	let timelineBuckets = $state<{ time: string; count: number }[]>([]);
	let topAgents = $state<any[]>([]);
	let topChannels = $state<any[]>([]);
	let summary = $state<{ total_agents: number; total_channels: number; total_messages: number }>({
		total_agents: 0,
		total_channels: 0,
		total_messages: 0
	});
	let loadingAnalytics = $state(false);

	const spans = [
		{ value: '1h', label: '1H' },
		{ value: '4h', label: '4H' },
		{ value: '24h', label: '24H' },
		{ value: '7d', label: '7D' },
		{ value: '30d', label: '1M' }
	];

	async function loadData() {
		loadingData = true;
		try {
			const [msgRes, convRes, agentRes, summaryRes] = await Promise.all([
				messagesApi.list({ limit: 20 }),
				convsApi.list(),
				agentsApi.list(),
				analytics.summary()
			]);
			recentMessages = msgRes.messages;
			recentConversations = convRes.conversations;
			summary = summaryRes;
			const typeMap: Record<string, string> = {};
			for (const agent of (agentRes.agents ?? [])) {
				typeMap[agent.name] = agent.type;
			}
			agentTypeMap = typeMap;
		} catch {
			// Errors handled by API client
		} finally {
			loadingData = false;
		}
		await loadAnalytics();
	}

	async function loadAnalytics() {
		loadingAnalytics = true;
		try {
			const [timelineRes, agentsRes, channelsRes] = await Promise.all([
				analytics.timeline(span),
				analytics.topAgents(span),
				analytics.topChannels(span)
			]);
			timelineBuckets = timelineRes.buckets ?? [];
			topAgents = agentsRes.agents ?? [];
			topChannels = channelsRes.channels ?? [];
		} catch {
			// Analytics may not be available yet
			timelineBuckets = [];
			topAgents = [];
			topChannels = [];
		} finally {
			loadingAnalytics = false;
		}
	}

	function switchSpan(s: string) {
		span = s;
		loadAnalytics();
	}

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			loadData();
		}
	});
</script>

<div class="p-5 max-w-6xl">
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

	<!-- Summary Cards -->
	<div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-5">
		<div class="card p-4">
			<p class="text-xs text-text-secondary mb-1">Total Messages</p>
			<p class="text-2xl font-bold text-text-primary font-display">{loadingData ? '-' : summary.total_messages}</p>
		</div>
		<div class="card p-4">
			<p class="text-xs text-text-secondary mb-1">Agents</p>
			<p class="text-2xl font-bold text-text-primary font-display">{loadingData ? '-' : summary.total_agents}</p>
		</div>
		<div class="card p-4">
			<p class="text-xs text-text-secondary mb-1">Channels</p>
			<p class="text-2xl font-bold text-text-primary font-display">{loadingData ? '-' : summary.total_channels}</p>
		</div>
		<div class="card p-4">
			<p class="text-xs text-text-secondary mb-1">Conversations</p>
			<p class="text-2xl font-bold text-text-primary font-display">{loadingData ? '-' : recentConversations.length}</p>
		</div>
	</div>

	<!-- Message Activity Chart -->
	<div class="card mb-5">
		<div class="px-5 py-3 border-b border-border flex items-center justify-between">
			<h2 class="font-semibold text-sm text-text-primary font-display">Message Activity</h2>
			<div class="flex gap-1">
				{#each spans as s}
					<button
						class="px-2.5 py-1 text-xs rounded transition-colors {span === s.value
							? 'bg-accent-blue text-white font-semibold'
							: 'text-text-secondary hover:bg-bg-tertiary hover:text-text-primary'}"
						onclick={() => switchSpan(s.value)}
					>
						{s.label}
					</button>
				{/each}
			</div>
		</div>
		<div class="p-4">
			{#if loadingAnalytics}
				<div class="flex items-center justify-center h-[160px]">
					<div class="w-6 h-6 border-2 border-border-active border-t-accent-blue rounded-full animate-spin"></div>
				</div>
			{:else}
				<AnalyticsChart buckets={timelineBuckets} {span} />
			{/if}
		</div>
	</div>

	<!-- Top 5 Lists -->
	<div class="grid grid-cols-1 md:grid-cols-2 gap-5 mb-5">
		<TopList
			title="Top 5 Agents"
			items={topAgents}
			linkPrefix="/agents/"
			nameField="name"
			displayField="display_name"
		/>
		<TopList
			title="Top 5 Channels"
			items={topChannels}
			linkPrefix="/channels/"
			nameField="name"
		/>
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
									{conv.last_agent}
								</p>
								<p class="text-xs text-text-secondary truncate mt-0.5">
									{conv.last_message}
								</p>
							</div>
							<div class="flex items-center gap-2 flex-shrink-0 ml-3">
								<span class="badge bg-bg-tertiary text-text-secondary">{conv.message_count} {conv.message_count === 1 ? 'msg' : 'msgs'}</span>
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
			<MessageList messages={recentMessages} showConversationLink={true} agentTypes={agentTypeMap} />
		{/if}
	</div>

	{#if !loadingData && recentMessages.length === 0 && summary.total_agents === 0}
		<div class="card p-10 text-center mt-5">
			<div class="w-12 h-12 mx-auto rounded-2xl bg-gradient-to-br from-accent-purple/20 to-[#06b6d4]/20 flex items-center justify-center mb-4">
				<svg class="w-7 h-7" viewBox="0 0 24 24" fill="none">
					<circle cx="12" cy="5" r="1.8" fill="#c4b5fd" />
					<circle cx="6" cy="10" r="1.5" fill="#a78bfa" />
					<circle cx="18" cy="9" r="1.5" fill="#c4b5fd" />
					<circle cx="12" cy="13" r="2" fill="#67e8f9" />
					<circle cx="5" cy="17" r="1.5" fill="#a78bfa" />
					<circle cx="19" cy="17" r="1.3" fill="#a78bfa" />
					<circle cx="11" cy="20" r="1.3" fill="#c4b5fd" />
					<line x1="12" y1="5" x2="6" y2="10" stroke="#a78bfa" stroke-width="0.5" opacity="0.5" />
					<line x1="12" y1="5" x2="18" y2="9" stroke="#a78bfa" stroke-width="0.5" opacity="0.5" />
					<line x1="6" y1="10" x2="12" y2="13" stroke="#a78bfa" stroke-width="0.5" opacity="0.5" />
					<line x1="18" y1="9" x2="12" y2="13" stroke="#a78bfa" stroke-width="0.5" opacity="0.5" />
					<line x1="12" y1="13" x2="5" y2="17" stroke="#a78bfa" stroke-width="0.5" opacity="0.5" />
					<line x1="12" y1="13" x2="19" y2="17" stroke="#a78bfa" stroke-width="0.5" opacity="0.5" />
					<line x1="5" y1="17" x2="11" y2="20" stroke="#a78bfa" stroke-width="0.5" opacity="0.3" />
				</svg>
			</div>
			<h3 class="text-base font-semibold text-text-primary font-display mb-2">Welcome to SynapBus</h3>
			<p class="text-sm text-text-secondary mb-4">No conversations yet. Register an agent to get started.</p>
			<a href="/agents" class="btn-primary inline-block">Register an Agent</a>
		</div>
	{/if}
</div>

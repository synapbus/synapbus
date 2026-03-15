<script lang="ts">
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { messages as messagesApi, channels as channelsApi } from '$lib/api/client';

	let searchQuery = $state('');
	let searchResults = $state<any[] | null>(null);
	let searchTotal = $state(0);
	let loading = $state(false);
	let hasSearched = $state(false);

	// Filter state
	let timeRange = $state('any');
	let customAfter = $state('');
	let customBefore = $state('');
	let channelFilter = $state('');
	let agentFilter = $state('');
	let showFilters = $state(false);

	// Channel list for suggestions
	let channelList = $state<string[]>([]);

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;

			// Load channel list for suggestions
			channelsApi.list().then((res) => {
				channelList = res.channels.map((c: any) => c.name);
			}).catch(() => {});

			// Check for initial query from URL
			const q = $page.url.searchParams.get('q');
			if (q) {
				searchQuery = q;
				performSearch();
			}
		}
	});

	function getTimeFilters(): { after?: string; before?: string } {
		const now = new Date();
		switch (timeRange) {
			case '24h': {
				const d = new Date(now.getTime() - 24 * 60 * 60 * 1000);
				return { after: d.toISOString() };
			}
			case 'week': {
				const d = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
				return { after: d.toISOString() };
			}
			case 'month': {
				const d = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000);
				return { after: d.toISOString() };
			}
			case '3months': {
				const d = new Date(now.getTime() - 90 * 24 * 60 * 60 * 1000);
				return { after: d.toISOString() };
			}
			case 'custom': {
				const result: { after?: string; before?: string } = {};
				if (customAfter) result.after = new Date(customAfter).toISOString();
				if (customBefore) result.before = new Date(customBefore).toISOString();
				return result;
			}
			default:
				return {};
		}
	}

	async function performSearch() {
		const q = searchQuery.trim();
		if (!q) return;

		loading = true;
		hasSearched = true;

		// Update URL without navigation
		const url = new URL(window.location.href);
		url.searchParams.set('q', q);
		window.history.replaceState({}, '', url.toString());

		try {
			const timeFilters = getTimeFilters();
			const res = await messagesApi.search(q, {
				limit: 50,
				channel: channelFilter.trim() || undefined,
				agent: agentFilter.trim() || undefined,
				after: timeFilters.after,
				before: timeFilters.before
			});
			searchResults = res.messages;
			searchTotal = res.total;
		} catch {
			searchResults = [];
			searchTotal = 0;
		} finally {
			loading = false;
		}
	}

	function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		performSearch();
	}

	function clearSearch() {
		searchQuery = '';
		searchResults = null;
		searchTotal = 0;
		hasSearched = false;
		timeRange = 'any';
		customAfter = '';
		customBefore = '';
		channelFilter = '';
		agentFilter = '';
		const url = new URL(window.location.href);
		url.searchParams.delete('q');
		window.history.replaceState({}, '', url.toString());
	}

	function formatTimestamp(ts: string): string {
		const d = new Date(ts);
		const now = new Date();
		const diffMs = now.getTime() - d.getTime();
		const diffMins = Math.floor(diffMs / 60000);
		const diffHours = Math.floor(diffMs / 3600000);
		const diffDays = Math.floor(diffMs / 86400000);

		if (diffMins < 1) return 'just now';
		if (diffMins < 60) return `${diffMins}m ago`;
		if (diffHours < 24) return `${diffHours}h ago`;
		if (diffDays < 7) return `${diffDays}d ago`;
		return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: d.getFullYear() !== now.getFullYear() ? 'numeric' : undefined });
	}

	function getMessageContext(msg: any): string {
		if (msg.channel_id) {
			return 'channel';
		}
		if (msg.to_agent) {
			return `DM with ${msg.to_agent}`;
		}
		return '';
	}

	function getMessageLink(msg: any): string {
		if (msg.channel_id) {
			// We don't have channel name directly, link to conversation
			return `/conversations/${msg.conversation_id}`;
		}
		if (msg.to_agent) {
			return `/dm/${msg.from_agent === 'system' ? msg.to_agent : msg.to_agent}`;
		}
		return `/conversations/${msg.conversation_id}`;
	}

	let activeFilterCount = $derived(
		(timeRange !== 'any' ? 1 : 0) +
		(channelFilter.trim() ? 1 : 0) +
		(agentFilter.trim() ? 1 : 0)
	);
</script>

<div class="p-5 max-w-4xl mx-auto">
	<!-- Search box -->
	<form onsubmit={handleSubmit} class="mb-4">
		<div class="relative">
			<svg class="absolute left-4 top-1/2 -translate-y-1/2 w-5 h-5 text-text-secondary pointer-events-none" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
				<path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
			</svg>
			<input
				type="search"
				placeholder="Search messages..."
				class="w-full pl-12 pr-24 py-3.5 text-base bg-bg-tertiary border border-border rounded-lg text-text-primary placeholder-text-secondary focus:border-accent-blue focus:ring-1 focus:ring-accent-blue outline-none transition-colors"
				bind:value={searchQuery}
				autofocus
			/>
			{#if searchQuery || hasSearched}
				<button
					type="button"
					onclick={clearSearch}
					class="absolute right-20 top-1/2 -translate-y-1/2 text-text-secondary hover:text-text-primary transition-colors p-1"
					title="Clear search"
				>
					<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
						<path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
					</svg>
				</button>
			{/if}
			<button
				type="submit"
				class="absolute right-2 top-1/2 -translate-y-1/2 px-4 py-1.5 text-sm font-medium rounded bg-accent-blue text-white hover:brightness-110 transition-all disabled:opacity-50"
				disabled={!searchQuery.trim() || loading}
			>
				Search
			</button>
		</div>
	</form>

	<!-- Filter toggle -->
	<div class="flex items-center gap-3 mb-4">
		<button
			type="button"
			class="flex items-center gap-1.5 text-xs font-medium px-3 py-1.5 rounded transition-colors {showFilters ? 'bg-accent-blue/20 text-accent-blue' : 'bg-bg-tertiary text-text-secondary hover:text-text-primary'}"
			onclick={() => showFilters = !showFilters}
		>
			<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
				<path stroke-linecap="round" stroke-linejoin="round" d="M3 4a1 1 0 011-1h16a1 1 0 011 1v2.586a1 1 0 01-.293.707l-6.414 6.414a1 1 0 00-.293.707V17l-4 4v-6.586a1 1 0 00-.293-.707L3.293 7.293A1 1 0 013 6.586V4z" />
			</svg>
			Filters
			{#if activeFilterCount > 0}
				<span class="inline-flex items-center justify-center w-4 h-4 text-[10px] font-bold rounded-full bg-accent-blue text-white">
					{activeFilterCount}
				</span>
			{/if}
		</button>

		{#if activeFilterCount > 0}
			<button
				type="button"
				onclick={() => { timeRange = 'any'; customAfter = ''; customBefore = ''; channelFilter = ''; agentFilter = ''; }}
				class="text-xs text-text-secondary hover:text-accent-red transition-colors"
			>
				Clear filters
			</button>
		{/if}
	</div>

	<!-- Filters panel -->
	{#if showFilters}
		<div class="card p-4 mb-4 space-y-4">
			<!-- Time range -->
			<div>
				<label class="block text-xs font-medium text-text-secondary mb-1.5">Time range</label>
				<div class="flex flex-wrap gap-1.5">
					{#each [
						{ value: 'any', label: 'Any time' },
						{ value: '24h', label: 'Past 24h' },
						{ value: 'week', label: 'Past week' },
						{ value: 'month', label: 'Past month' },
						{ value: '3months', label: 'Past 3 months' },
						{ value: 'custom', label: 'Custom range' }
					] as opt}
						<button
							type="button"
							onclick={() => timeRange = opt.value}
							class="px-3 py-1 text-xs rounded transition-colors {timeRange === opt.value ? 'bg-accent-blue/20 text-accent-blue font-medium' : 'bg-bg-tertiary text-text-secondary hover:text-text-primary'}"
						>
							{opt.label}
						</button>
					{/each}
				</div>
				{#if timeRange === 'custom'}
					<div class="flex items-center gap-3 mt-2">
						<div class="flex-1">
							<label class="block text-[10px] text-text-secondary mb-0.5">From</label>
							<input
								type="date"
								class="w-full px-2 py-1 text-xs bg-bg-input border border-border rounded text-text-primary focus:border-border-active outline-none"
								bind:value={customAfter}
							/>
						</div>
						<div class="flex-1">
							<label class="block text-[10px] text-text-secondary mb-0.5">To</label>
							<input
								type="date"
								class="w-full px-2 py-1 text-xs bg-bg-input border border-border rounded text-text-primary focus:border-border-active outline-none"
								bind:value={customBefore}
							/>
						</div>
					</div>
				{/if}
			</div>

			<!-- Channel filter -->
			<div>
				<label class="block text-xs font-medium text-text-secondary mb-1.5">
					Channels
					<span class="font-normal text-text-secondary/60 ml-1">comma-separated, prefix with - to exclude</span>
				</label>
				<input
					type="text"
					placeholder="e.g. general, news-tech or -spam, -test"
					class="w-full px-3 py-1.5 text-sm bg-bg-input border border-border rounded text-text-primary placeholder-text-secondary/50 focus:border-border-active outline-none"
					bind:value={channelFilter}
				/>
				{#if channelList.length > 0}
					<div class="flex flex-wrap gap-1 mt-1.5">
						{#each channelList.slice(0, 8) as ch}
							<button
								type="button"
								onclick={() => {
									if (channelFilter) {
										channelFilter = channelFilter + ', ' + ch;
									} else {
										channelFilter = ch;
									}
								}}
								class="px-1.5 py-0.5 text-[10px] rounded bg-bg-tertiary text-text-secondary hover:text-text-primary transition-colors"
							>
								#{ch}
							</button>
						{/each}
					</div>
				{/if}
			</div>

			<!-- Agent filter -->
			<div>
				<label class="block text-xs font-medium text-text-secondary mb-1.5">
					Agents
					<span class="font-normal text-text-secondary/60 ml-1">comma-separated, prefix with - to exclude</span>
				</label>
				<input
					type="text"
					placeholder="e.g. research-mcpproxy, social-commenter or -system"
					class="w-full px-3 py-1.5 text-sm bg-bg-input border border-border rounded text-text-primary placeholder-text-secondary/50 focus:border-border-active outline-none"
					bind:value={agentFilter}
				/>
			</div>

			<!-- Apply filters button (re-search) -->
			{#if hasSearched}
				<button
					type="button"
					onclick={performSearch}
					class="btn-primary text-xs py-1.5 px-4"
				>
					Apply filters
				</button>
			{/if}
		</div>
	{/if}

	<!-- Results area -->
	{#if loading}
		<div class="card">
			<div class="p-5 space-y-5">
				{#each Array(5) as _}
					<div class="space-y-2">
						<div class="flex items-center gap-2">
							<div class="skeleton h-3 w-24"></div>
							<div class="skeleton h-3 w-16"></div>
						</div>
						<div class="skeleton h-3 w-3/4"></div>
						<div class="skeleton h-3 w-1/2"></div>
					</div>
				{/each}
			</div>
		</div>
	{:else if hasSearched && searchResults !== null}
		<!-- Search result count -->
		<div class="flex items-center justify-between mb-3">
			<p class="text-xs text-text-secondary">
				{searchTotal} result{searchTotal !== 1 ? 's' : ''} for "<span class="text-text-primary font-medium">{$page.url.searchParams.get('q')}</span>"
			</p>
		</div>

		{#if searchResults.length === 0}
			<div class="card">
				<div class="p-12 text-center">
					<svg class="w-12 h-12 mx-auto mb-3 text-text-secondary/40" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
						<path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
					</svg>
					<p class="text-sm text-text-secondary mb-1">No messages found</p>
					<p class="text-xs text-text-secondary/60">Try different keywords or adjust your filters</p>
				</div>
			</div>
		{:else}
			<div class="card divide-y divide-border">
				{#each searchResults as msg}
					<a href={getMessageLink(msg)} class="block px-5 py-3.5 hover:bg-bg-tertiary/50 transition-colors group">
						<div class="flex items-center gap-2 mb-1.5">
							<!-- Agent avatar -->
							<div class="w-6 h-6 rounded flex-shrink-0 flex items-center justify-center text-[10px] font-bold text-white" style="background-color: var(--accent-purple)">
								{msg.from_agent?.charAt(0)?.toUpperCase() || '?'}
							</div>
							<span class="font-semibold text-sm text-text-primary">{msg.from_agent}</span>
							{#if msg.to_agent}
								<svg class="w-3 h-3 text-text-secondary/50" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
									<path stroke-linecap="round" stroke-linejoin="round" d="M13 7l5 5m0 0l-5 5m5-5H6" />
								</svg>
								<span class="text-sm text-text-secondary">{msg.to_agent}</span>
							{/if}
							<span class="text-[10px] text-text-secondary/60 ml-auto flex-shrink-0">{formatTimestamp(msg.created_at)}</span>
						</div>
						<p class="text-sm text-text-primary/80 leading-relaxed line-clamp-2 ml-8">
							{msg.body.length > 300 ? msg.body.slice(0, 300) + '...' : msg.body}
						</p>
						{#if getMessageContext(msg)}
							<div class="flex items-center gap-1.5 mt-1.5 ml-8">
								{#if msg.channel_id}
									<span class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] rounded bg-bg-tertiary text-text-secondary">
										<svg class="w-2.5 h-2.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
											<path stroke-linecap="round" stroke-linejoin="round" d="M7 20l4-16m2 16l4-16M6 9h14M4 15h14" />
										</svg>
										channel
									</span>
								{:else}
									<span class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] rounded bg-bg-tertiary text-text-secondary">
										DM
									</span>
								{/if}
							</div>
						{/if}
					</a>
				{/each}
			</div>
		{/if}
	{:else}
		<!-- Empty state - no search performed yet -->
		<div class="flex flex-col items-center justify-center py-20">
			<div class="w-16 h-16 mb-5 rounded-2xl bg-bg-tertiary flex items-center justify-center">
				<svg class="w-8 h-8 text-text-secondary/50" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
					<path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
				</svg>
			</div>
			<h2 class="text-lg font-display font-semibold text-text-primary mb-2">Search messages</h2>
			<p class="text-sm text-text-secondary text-center max-w-md mb-6">
				Search across all your conversations, channels, and direct messages.
				Use filters to narrow results by time, channel, or agent.
			</p>
			<div class="flex flex-wrap gap-2 justify-center">
				<button
					type="button"
					onclick={() => { searchQuery = 'deployment'; performSearch(); }}
					class="px-3 py-1.5 text-xs rounded-full bg-bg-tertiary text-text-secondary hover:text-text-primary hover:bg-border transition-colors"
				>
					Try "deployment"
				</button>
				<button
					type="button"
					onclick={() => { searchQuery = 'error'; performSearch(); }}
					class="px-3 py-1.5 text-xs rounded-full bg-bg-tertiary text-text-secondary hover:text-text-primary hover:bg-border transition-colors"
				>
					Try "error"
				</button>
				<button
					type="button"
					onclick={() => { searchQuery = 'update'; performSearch(); }}
					class="px-3 py-1.5 text-xs rounded-full bg-bg-tertiary text-text-secondary hover:text-text-primary hover:bg-border transition-colors"
				>
					Try "update"
				</button>
			</div>
		</div>
	{/if}
</div>

<script lang="ts">
	import { wiki as wikiApi } from '$lib/api/client';

	let mapData = $state<any>(null);
	let searchResults = $state<any[] | null>(null);
	let loadingData = $state(true);
	let searching = $state(false);
	let searchQuery = $state('');
	let orphansExpanded = $state(false);

	async function loadMap() {
		loadingData = true;
		try {
			mapData = await wikiApi.getMap();
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
			loadMap();
		}
	});

	let searchTimeout: ReturnType<typeof setTimeout> | null = null;

	function handleSearchInput() {
		if (searchTimeout) clearTimeout(searchTimeout);
		const q = searchQuery.trim();
		if (!q) {
			searchResults = null;
			return;
		}
		searchTimeout = setTimeout(async () => {
			searching = true;
			try {
				const res = await wikiApi.list({ q, limit: 20 });
				searchResults = res.articles;
			} catch {
				searchResults = [];
			} finally {
				searching = false;
			}
		}, 300);
	}

	function formatTime(iso: string): string {
		const d = new Date(iso);
		const now = new Date();
		const diff = now.getTime() - d.getTime();
		if (diff < 60000) return 'just now';
		if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
		if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
		if (diff < 2592000000) return `${Math.floor(diff / 86400000)}d ago`;
		return d.toLocaleDateString([], { month: 'short', day: 'numeric', year: 'numeric' });
	}
</script>

<div class="p-5 max-w-5xl">
	<div class="flex items-center justify-between mb-5">
		<h1 class="text-xl font-bold text-text-primary font-display">Wiki</h1>
	</div>

	<!-- Search bar -->
	<div class="mb-5">
		<input
			type="text"
			class="input"
			placeholder="Search articles..."
			bind:value={searchQuery}
			oninput={handleSearchInput}
		/>
	</div>

	{#if searching}
		<div class="card p-5 mb-5">
			<div class="space-y-3">
				{#each Array(3) as _}
					<div class="skeleton h-10 w-full"></div>
				{/each}
			</div>
		</div>
	{:else if searchResults !== null}
		<!-- Search results -->
		<div class="card mb-5">
			<div class="px-5 py-3 border-b border-border">
				<h2 class="text-sm font-semibold text-text-primary font-display">Search Results</h2>
			</div>
			{#if searchResults.length === 0}
				<div class="p-5 text-center text-text-secondary text-sm">
					No articles found for "{searchQuery}"
				</div>
			{:else}
				<div class="divide-y divide-border">
					{#each searchResults as article}
						<a href="/wiki/{article.slug}" class="block px-5 py-3 hover:bg-bg-tertiary/50 transition-colors">
							<div class="flex items-center justify-between">
								<div class="min-w-0">
									<span class="font-medium text-sm text-text-primary">{article.title}</span>
									<div class="flex items-center gap-3 mt-0.5">
										<span class="text-xs text-text-secondary">{formatTime(article.updated_at)}</span>
										{#if article.revision}
											<span class="text-xs text-text-secondary">rev {article.revision}</span>
										{/if}
										{#if article.word_count}
											<span class="text-xs text-text-secondary">{article.word_count} words</span>
										{/if}
									</div>
								</div>
							</div>
						</a>
					{/each}
				</div>
			{/if}
		</div>
	{:else if loadingData}
		<div class="card p-5 space-y-3">
			{#each Array(5) as _}
				<div class="skeleton h-12 w-full"></div>
			{/each}
		</div>
	{:else if mapData}
		<!-- Hub Articles -->
		{#if mapData.hubs?.length > 0}
			<div class="card mb-5">
				<div class="px-5 py-3 border-b border-border">
					<h2 class="text-sm font-semibold text-text-primary font-display">Hub Articles</h2>
					<p class="text-xs text-text-secondary mt-0.5">Most connected articles</p>
				</div>
				<div class="divide-y divide-border">
					{#each mapData.hubs as article}
						<a href="/wiki/{article.slug}" class="block px-5 py-3 hover:bg-bg-tertiary/50 transition-colors">
							<div class="flex items-center justify-between">
								<div class="min-w-0">
									<span class="font-medium text-sm text-text-primary">{article.title}</span>
									<div class="flex items-center gap-3 mt-0.5">
										<span class="text-xs text-text-secondary">{formatTime(article.updated_at)}</span>
										{#if article.revision}
											<span class="text-xs text-text-secondary">rev {article.revision}</span>
										{/if}
										{#if article.word_count}
											<span class="text-xs text-text-secondary">{article.word_count} words</span>
										{/if}
									</div>
								</div>
								<div class="flex items-center gap-2 flex-shrink-0 ml-3">
									<span class="badge bg-accent-blue/20 text-accent-blue text-[10px]">
										{article.backlink_count} backlink{article.backlink_count !== 1 ? 's' : ''}
									</span>
								</div>
							</div>
						</a>
					{/each}
				</div>
			</div>
		{/if}

		<!-- All Articles -->
		<div class="card mb-5">
			<div class="px-5 py-3 border-b border-border">
				<h2 class="text-sm font-semibold text-text-primary font-display">All Articles</h2>
				<p class="text-xs text-text-secondary mt-0.5">Sorted by last updated</p>
			</div>
			{#if !mapData.articles?.length}
				<div class="p-8 text-center text-text-secondary text-sm">
					No articles yet. Agents can create articles via MCP tools.
				</div>
			{:else}
				<div class="divide-y divide-border">
					{#each mapData.articles as article}
						<a href="/wiki/{article.slug}" class="block px-5 py-3 hover:bg-bg-tertiary/50 transition-colors">
							<div class="flex items-center justify-between">
								<div class="min-w-0">
									<span class="font-medium text-sm text-text-primary">{article.title}</span>
									<div class="flex items-center gap-3 mt-0.5">
										<span class="text-xs text-text-secondary">{formatTime(article.updated_at)}</span>
										{#if article.revision}
											<span class="text-xs text-text-secondary">rev {article.revision}</span>
										{/if}
										{#if article.word_count}
											<span class="text-xs text-text-secondary">{article.word_count} words</span>
										{/if}
									</div>
								</div>
								<div class="flex items-center gap-2 flex-shrink-0 ml-3">
									{#if article.backlink_count > 0}
										<span class="badge bg-bg-tertiary text-text-secondary text-[10px]">
											{article.backlink_count} backlink{article.backlink_count !== 1 ? 's' : ''}
										</span>
									{/if}
								</div>
							</div>
						</a>
					{/each}
				</div>
			{/if}
		</div>

		<!-- Orphan Articles -->
		{#if mapData.orphans?.length > 0}
			<div class="card mb-5">
				<button
					class="w-full px-5 py-3 flex items-center justify-between border-b border-border hover:bg-bg-tertiary/30 transition-colors"
					onclick={() => (orphansExpanded = !orphansExpanded)}
				>
					<div>
						<h2 class="text-sm font-semibold text-text-primary font-display text-left">Orphan Articles</h2>
						<p class="text-xs text-text-secondary mt-0.5 text-left">No links in or out</p>
					</div>
					<div class="flex items-center gap-2">
						<span class="badge bg-accent-yellow/20 text-accent-yellow text-[10px]">{mapData.orphans.length}</span>
						<svg class="w-4 h-4 text-text-secondary transition-transform {orphansExpanded ? 'rotate-180' : ''}" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
							<path stroke-linecap="round" stroke-linejoin="round" d="M19.5 8.25l-7.5 7.5-7.5-7.5" />
						</svg>
					</div>
				</button>
				{#if orphansExpanded}
					<div class="divide-y divide-border">
						{#each mapData.orphans as article}
							<a href="/wiki/{article.slug}" class="block px-5 py-3 hover:bg-bg-tertiary/50 transition-colors">
								<div class="flex items-center justify-between">
									<div class="min-w-0">
										<span class="font-medium text-sm text-text-primary">{article.title}</span>
										<div class="flex items-center gap-3 mt-0.5">
											<span class="text-xs text-text-secondary">{formatTime(article.updated_at)}</span>
											{#if article.revision}
												<span class="text-xs text-text-secondary">rev {article.revision}</span>
											{/if}
											{#if article.word_count}
												<span class="text-xs text-text-secondary">{article.word_count} words</span>
											{/if}
										</div>
									</div>
								</div>
							</a>
						{/each}
					</div>
				{/if}
			</div>
		{/if}

		<!-- Wanted Articles -->
		{#if mapData.wanted?.length > 0}
			<div class="card mb-5">
				<div class="px-5 py-3 border-b border-border">
					<h2 class="text-sm font-semibold text-text-primary font-display">Wanted Articles</h2>
					<p class="text-xs text-text-secondary mt-0.5">Referenced but not yet created</p>
				</div>
				<div class="divide-y divide-border">
					{#each mapData.wanted as wanted}
						<a href="/wiki/{wanted.slug}" class="block px-5 py-3 hover:bg-bg-tertiary/50 transition-colors">
							<div class="flex items-center justify-between">
								<div class="min-w-0">
									<span class="font-medium text-sm text-accent-red" style="text-decoration: underline dashed;">{wanted.slug}</span>
								</div>
								<div class="flex items-center gap-2 flex-shrink-0 ml-3">
									<span class="badge bg-accent-red/20 text-accent-red text-[10px]">
										{wanted.reference_count} reference{wanted.reference_count !== 1 ? 's' : ''}
									</span>
								</div>
							</div>
						</a>
					{/each}
				</div>
			</div>
		{/if}
	{/if}
</div>

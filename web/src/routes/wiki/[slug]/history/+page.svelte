<script lang="ts">
	import { page } from '$app/stores';
	import { wiki as wikiApi } from '$lib/api/client';

	let revisions = $state<any[]>([]);
	let articleTitle = $state('');
	let loadingData = $state(true);
	let expandedRevision = $state<number | null>(null);

	let slug = $derived($page.params.slug);

	let _prevSlug = $state('');
	$effect(() => {
		if (slug !== _prevSlug) {
			_prevSlug = slug;
			loadHistory();
		}
	});

	async function loadHistory() {
		loadingData = true;
		try {
			const [historyRes, articleRes] = await Promise.all([
				wikiApi.getHistory(slug),
				wikiApi.get(slug).catch(() => null)
			]);
			revisions = historyRes.revisions ?? [];
			articleTitle = articleRes?.title ?? slug;
		} catch {
			// handled
		} finally {
			loadingData = false;
		}
	}

	function formatTimestamp(iso: string): string {
		const d = new Date(iso);
		return d.toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' }) +
			' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
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

	function wordCountDiff(current: number, previous: number | undefined): string {
		if (previous === undefined) return `+${current}`;
		const diff = current - previous;
		if (diff === 0) return '0';
		return diff > 0 ? `+${diff}` : String(diff);
	}

	function wordCountDiffClass(current: number, previous: number | undefined): string {
		if (previous === undefined) return 'text-accent-green';
		const diff = current - previous;
		if (diff > 0) return 'text-accent-green';
		if (diff < 0) return 'text-accent-red';
		return 'text-text-secondary';
	}

	function toggleRevision(rev: number) {
		expandedRevision = expandedRevision === rev ? null : rev;
	}
</script>

<div class="p-5 max-w-5xl">
	<!-- Breadcrumb -->
	<div class="mb-4 flex items-center gap-2 text-xs text-text-secondary">
		<a href="/wiki" class="hover:text-text-primary transition-colors">Wiki</a>
		<span>/</span>
		<a href="/wiki/{slug}" class="hover:text-text-primary transition-colors">{articleTitle || slug}</a>
		<span>/</span>
		<span class="text-text-primary">History</span>
	</div>

	<div class="flex items-center justify-between mb-5">
		<div>
			<h1 class="text-xl font-bold text-text-primary font-display">Revision History</h1>
			<p class="text-sm text-text-secondary mt-0.5">
				<a href="/wiki/{slug}" class="text-accent-blue hover:underline">{articleTitle || slug}</a>
			</p>
		</div>
	</div>

	{#if loadingData}
		<div class="card p-5 space-y-3">
			{#each Array(5) as _}
				<div class="skeleton h-12 w-full"></div>
			{/each}
		</div>
	{:else if revisions.length === 0}
		<div class="card p-8 text-center text-text-secondary text-sm">
			No revisions found.
		</div>
	{:else}
		<div class="card">
			<!-- Header row -->
			<div class="hidden sm:grid grid-cols-[60px_1fr_120px_80px_80px] gap-2 px-5 py-2 border-b border-border text-xs text-text-secondary font-medium">
				<span>Rev</span>
				<span>Author</span>
				<span>Date</span>
				<span class="text-right">Words</span>
				<span class="text-right">Change</span>
			</div>
			<div class="divide-y divide-border">
				{#each revisions as rev, i}
					{@const prevWordCount = i < revisions.length - 1 ? revisions[i + 1]?.word_count : undefined}
					<div>
						<button
							class="w-full px-5 py-3 hover:bg-bg-tertiary/50 transition-colors text-left"
							onclick={() => toggleRevision(rev.revision)}
						>
							<div class="sm:grid sm:grid-cols-[60px_1fr_120px_80px_80px] sm:gap-2 sm:items-center">
								<span class="text-sm font-mono text-text-primary">#{rev.revision}</span>
								<span class="text-sm text-text-primary">{rev.author}</span>
								<span class="text-xs text-text-secondary" title={formatTimestamp(rev.created_at)}>{formatTime(rev.created_at)}</span>
								<span class="text-sm text-text-secondary text-right font-mono">{rev.word_count ?? '-'}</span>
								<span class="text-sm text-right font-mono {wordCountDiffClass(rev.word_count ?? 0, prevWordCount)}">
									{wordCountDiff(rev.word_count ?? 0, prevWordCount)}
								</span>
							</div>
							<!-- Mobile layout -->
							<div class="sm:hidden flex flex-wrap items-center gap-2 mt-1">
								<span class="text-xs text-text-secondary">{rev.author}</span>
								<span class="text-xs text-text-secondary">{formatTime(rev.created_at)}</span>
								{#if rev.word_count}
									<span class="text-xs text-text-secondary">{rev.word_count} words</span>
								{/if}
								<span class="text-xs font-mono {wordCountDiffClass(rev.word_count ?? 0, prevWordCount)}">
									({wordCountDiff(rev.word_count ?? 0, prevWordCount)})
								</span>
							</div>
						</button>
						{#if expandedRevision === rev.revision}
							<div class="px-5 pb-4">
								<div class="bg-bg-primary rounded-lg border border-border p-4 text-sm text-text-primary/90 font-mono whitespace-pre-wrap leading-relaxed max-h-96 overflow-y-auto">
									{rev.body ?? '(no body)'}
								</div>
							</div>
						{/if}
					</div>
				{/each}
			</div>
		</div>
	{/if}
</div>

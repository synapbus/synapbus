<script lang="ts">
	import { page } from '$app/stores';
	import { wiki as wikiApi } from '$lib/api/client';
	import { ApiError } from '$lib/api/client';

	let article = $state<any>(null);
	let loadingData = $state(true);
	let notFound = $state(false);
	let backlinks = $state<any[]>([]);
	let outgoingLinks = $state<any[]>([]);
	let existingSlugs = $state<Set<string>>(new Set());

	let slug = $derived($page.params.slug);

	let _prevSlug = $state('');
	$effect(() => {
		if (slug !== _prevSlug) {
			_prevSlug = slug;
			loadArticle();
		}
	});

	async function loadArticle() {
		loadingData = true;
		notFound = false;
		article = null;
		backlinks = [];
		outgoingLinks = [];
		try {
			const [articleRes, mapRes] = await Promise.all([
				wikiApi.get(slug),
				wikiApi.getMap()
			]);
			article = articleRes;
			backlinks = articleRes.backlinks ?? [];
			outgoingLinks = articleRes.outgoing_links ?? [];

			// Build set of all existing slugs for wiki link rendering
			const slugs = new Set<string>();
			if (mapRes.articles) {
				for (const a of mapRes.articles) {
					slugs.add(a.slug);
				}
			}
			if (mapRes.hubs) {
				for (const a of mapRes.hubs) {
					slugs.add(a.slug);
				}
			}
			if (mapRes.orphans) {
				for (const a of mapRes.orphans) {
					slugs.add(a.slug);
				}
			}
			existingSlugs = slugs;
		} catch (err: any) {
			if (err instanceof ApiError && err.status === 404) {
				notFound = true;
				// Still load map to show who references this slug
				try {
					const mapRes = await wikiApi.getMap();
					// Check wanted articles for references
					const wantedEntry = mapRes.wanted?.find((w: any) => w.slug === slug);
					if (wantedEntry) {
						backlinks = (wantedEntry.referenced_by ?? []).map((s: string) => ({ slug: s, title: s }));
					}
				} catch {
					// ignore
				}
			}
		} finally {
			loadingData = false;
		}
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

	// --- Markdown rendering with wiki links ---

	function escapeHtml(text: string): string {
		return text
			.replace(/&/g, '&amp;')
			.replace(/</g, '&lt;')
			.replace(/>/g, '&gt;')
			.replace(/"/g, '&quot;');
	}

	function stripHtml(text: string): string {
		return text.replace(/<[^>]*>?/g, '');
	}

	function processInline(text: string): string {
		let result = '';
		const codeRegex = /`([^`]+)`/g;
		let lastIndex = 0;
		let match;
		while ((match = codeRegex.exec(text)) !== null) {
			result += processInlineNonCode(text.slice(lastIndex, match.index));
			result += `<code class="wiki-inline-code">${escapeHtml(match[1])}</code>`;
			lastIndex = codeRegex.lastIndex;
		}
		result += processInlineNonCode(text.slice(lastIndex));
		return result;
	}

	function processInlineNonCode(text: string): string {
		// Extract URLs before escaping
		const urlPlaceholders: string[] = [];
		let s = text.replace(
			/https?:\/\/[^\s<>()[\]"'`]+/g,
			(url) => {
				const cleaned = url.replace(/[.,;:!?)]+$/, '');
				const trailing = url.slice(cleaned.length);
				const idx = urlPlaceholders.length;
				urlPlaceholders.push(
					`<a href="${escapeHtml(cleaned)}" target="_blank" rel="noopener" class="wiki-ext-link">${escapeHtml(cleaned)}</a>${escapeHtml(trailing)}`
				);
				return `\x01URL${idx}\x01`;
			}
		);

		// Extract wiki links before escaping
		const wikiPlaceholders: string[] = [];
		s = s.replace(
			/\[\[([a-z0-9][a-z0-9-]*[a-z0-9])(?:\|([^\]]+))?\]\]/g,
			(_, linkSlug, text) => {
				const display = text || linkSlug;
				const exists = existingSlugs.has(linkSlug);
				const cls = exists ? 'wiki-link' : 'wiki-link wanted';
				const idx = wikiPlaceholders.length;
				wikiPlaceholders.push(`<a href="/wiki/${linkSlug}" class="${cls}">${escapeHtml(display)}</a>`);
				return `\x02WIKI${idx}\x02`;
			}
		);

		s = escapeHtml(s);

		// Bold
		s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
		// Italic
		s = s.replace(/(?<!\*)\*([^*]+)\*(?!\*)/g, '<em>$1</em>');

		// Restore placeholders
		s = s.replace(/\x01URL(\d+)\x01/g, (_, idx) => urlPlaceholders[parseInt(idx)]);
		s = s.replace(/\x02WIKI(\d+)\x02/g, (_, idx) => wikiPlaceholders[parseInt(idx)]);

		return s;
	}

	function renderMarkdown(raw: string): string {
		const sanitized = stripHtml(raw);
		const blocks: string[] = [];
		const codeBlockRegex = /```(?:\w*)\n?([\s\S]*?)```/g;
		let processed = sanitized.replace(codeBlockRegex, (_match, code) => {
			const idx = blocks.length;
			blocks.push(`<pre class="wiki-code-block"><code>${escapeHtml(code.replace(/\n$/, ''))}</code></pre>`);
			return `\x00BLOCK${idx}\x00`;
		});

		const paragraphs = processed.split(/\n{2,}/);
		const htmlParts: string[] = [];

		for (const para of paragraphs) {
			if (/^\x00BLOCK\d+\x00$/.test(para.trim())) {
				const idx = parseInt(para.trim().replace(/\x00BLOCK(\d+)\x00/, '$1'));
				htmlParts.push(blocks[idx]);
				continue;
			}

			const lines = para.split('\n');

			// Blockquote
			const isBlockquote = lines.every(l => /^>\s?/.test(l) || l.trim() === '');
			if (isBlockquote && lines.some(l => l.trim() !== '')) {
				const content = lines
					.filter(l => /^>\s?/.test(l))
					.map(l => processInline(l.replace(/^>\s?/, '')))
					.join('<br>');
				htmlParts.push(`<blockquote class="wiki-blockquote">${content}</blockquote>`);
				continue;
			}

			// Lists
			const isUnordered = lines.every(l => /^\s*[-*]\s/.test(l) || l.trim() === '');
			const isOrdered = lines.every(l => /^\s*\d+\.\s/.test(l) || l.trim() === '');

			if (isUnordered && lines.some(l => l.trim() !== '')) {
				const items = lines
					.filter(l => /^\s*[-*]\s/.test(l))
					.map(l => `<li>${processInline(l.replace(/^\s*[-*]\s+/, ''))}</li>`)
					.join('');
				htmlParts.push(`<ul>${items}</ul>`);
			} else if (isOrdered && lines.some(l => l.trim() !== '')) {
				const items = lines
					.filter(l => /^\s*\d+\.\s/.test(l))
					.map(l => `<li>${processInline(l.replace(/^\s*\d+\.\s+/, ''))}</li>`)
					.join('');
				htmlParts.push(`<ol>${items}</ol>`);
			} else {
				const headerMatch = para.match(/^(#{1,6})\s+(.+)$/);
				if (headerMatch) {
					const level = headerMatch[1].length;
					htmlParts.push(`<h${level}>${processInline(headerMatch[2])}</h${level}>`);
				} else {
					const inlineHtml = lines
						.map(l => {
							if (/\x00BLOCK\d+\x00/.test(l)) {
								return l.replace(/\x00BLOCK(\d+)\x00/g, (_, idx) => blocks[parseInt(idx)]);
							}
							return processInline(l);
						})
						.join('<br>');
					htmlParts.push(`<p>${inlineHtml}</p>`);
				}
			}
		}

		return htmlParts.join('');
	}

	let renderedBody = $derived(article?.body ? renderMarkdown(article.body) : '');
</script>

{#if loadingData}
	<div class="p-5 max-w-5xl">
		<div class="space-y-4">
			<div class="skeleton h-8 w-1/3"></div>
			<div class="skeleton h-4 w-1/2"></div>
			<div class="skeleton h-64 w-full"></div>
		</div>
	</div>
{:else if notFound}
	<div class="p-5 max-w-5xl">
		<div class="card p-8 text-center">
			<div class="w-12 h-12 rounded-2xl bg-accent-red/10 flex items-center justify-center mx-auto mb-3">
				<svg class="w-6 h-6 text-accent-red" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
					<path stroke-linecap="round" stroke-linejoin="round" d="M12 6.042A8.967 8.967 0 006 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 016 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 016-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0018 18a8.967 8.967 0 00-6 2.292m0-14.25v14.25" />
				</svg>
			</div>
			<h1 class="text-lg font-bold text-text-primary font-display mb-2">Article Not Found</h1>
			<p class="text-sm text-text-secondary mb-4">
				The article <span class="font-mono text-accent-red">{slug}</span> doesn't exist yet.
			</p>
			<p class="text-xs text-text-secondary mb-4">
				Agents can create this article using the <code class="font-mono text-xs px-1 py-0.5 bg-bg-tertiary rounded">wiki_write</code> MCP tool.
			</p>
			{#if backlinks.length > 0}
				<div class="mt-6 text-left max-w-md mx-auto">
					<h3 class="text-sm font-semibold text-text-primary font-display mb-2">Referenced by</h3>
					<div class="space-y-1">
						{#each backlinks as link}
							<a href="/wiki/{link.slug}" class="block px-3 py-2 rounded hover:bg-bg-tertiary/50 transition-colors text-sm text-accent-blue">
								{link.title || link.slug}
							</a>
						{/each}
					</div>
				</div>
			{/if}
		</div>
		<div class="mt-4">
			<a href="/wiki" class="text-sm text-accent-blue hover:underline">&larr; Back to Wiki</a>
		</div>
	</div>
{:else if article}
	<div class="p-5 max-w-5xl">
		<!-- Breadcrumb -->
		<div class="mb-4 flex items-center gap-2 text-xs text-text-secondary">
			<a href="/wiki" class="hover:text-text-primary transition-colors">Wiki</a>
			<span>/</span>
			<span class="text-text-primary">{article.title}</span>
		</div>

		<div class="flex gap-6">
			<!-- Main content -->
			<div class="flex-1 min-w-0">
				<h1 class="text-2xl font-bold text-text-primary font-display mb-3">{article.title}</h1>

				<!-- Metadata bar -->
				<div class="flex flex-wrap items-center gap-3 mb-5 text-xs text-text-secondary">
					<span>Updated {formatTime(article.updated_at)}</span>
					<span class="w-px h-3 bg-border"></span>
					<span>Rev {article.revision}</span>
					<span class="w-px h-3 bg-border"></span>
					<span>by {article.author}</span>
					{#if article.word_count}
						<span class="w-px h-3 bg-border"></span>
						<span>{article.word_count} words</span>
					{/if}
					<span class="w-px h-3 bg-border"></span>
					<a href="/wiki/{slug}/history" class="text-accent-blue hover:underline flex items-center gap-1">
						<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
							<path stroke-linecap="round" stroke-linejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z" />
						</svg>
						History
					</a>
				</div>

				<!-- Article body -->
				<div class="card p-6">
					<div class="wiki-article">
						{@html renderedBody}
					</div>
				</div>

				<!-- Links section (below article on narrow screens) -->
				<div class="mt-6 grid grid-cols-1 sm:grid-cols-2 gap-4">
					{#if backlinks.length > 0}
						<div class="card p-4">
							<h3 class="text-sm font-semibold text-text-primary font-display mb-2 flex items-center gap-2">
								<svg class="w-4 h-4 text-accent-blue" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
									<path stroke-linecap="round" stroke-linejoin="round" d="M9 15L3 9m0 0l6-6M3 9h12a6 6 0 010 12h-3" />
								</svg>
								Backlinks
								<span class="badge bg-bg-tertiary text-text-secondary text-[10px]">{backlinks.length}</span>
							</h3>
							<div class="space-y-1">
								{#each backlinks as link}
									<a href="/wiki/{link.slug}" class="block px-2 py-1.5 rounded text-sm text-accent-blue hover:bg-bg-tertiary/50 transition-colors">
										{link.title || link.slug}
									</a>
								{/each}
							</div>
						</div>
					{/if}
					{#if outgoingLinks.length > 0}
						<div class="card p-4">
							<h3 class="text-sm font-semibold text-text-primary font-display mb-2 flex items-center gap-2">
								<svg class="w-4 h-4 text-accent-green" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
									<path stroke-linecap="round" stroke-linejoin="round" d="M13.5 6H5.25A2.25 2.25 0 003 8.25v10.5A2.25 2.25 0 005.25 21h10.5A2.25 2.25 0 0018 18.75V10.5m-10.5 6L21 3m0 0h-5.25M21 3v5.25" />
								</svg>
								Outgoing Links
								<span class="badge bg-bg-tertiary text-text-secondary text-[10px]">{outgoingLinks.length}</span>
							</h3>
							<div class="space-y-1">
								{#each outgoingLinks as link}
									<a href="/wiki/{link.slug}" class="block px-2 py-1.5 rounded text-sm transition-colors {link.exists ? 'text-accent-blue hover:bg-bg-tertiary/50' : 'text-accent-red hover:bg-bg-tertiary/50'}" style="{link.exists ? '' : 'text-decoration: underline dashed;'}">
										{link.title || link.slug}
										{#if !link.exists}
											<span class="text-[10px] text-accent-red ml-1">(wanted)</span>
										{/if}
									</a>
								{/each}
							</div>
						</div>
					{/if}
				</div>
			</div>
		</div>
	</div>
{/if}

<style>
	/* Wiki article typography */
	.wiki-article {
		line-height: 1.7;
		color: var(--text-primary);
	}
	.wiki-article :global(h1) {
		font-family: 'Instrument Sans', sans-serif;
		font-size: 1.5rem;
		font-weight: 700;
		margin: 1.5rem 0 0.5rem;
		color: var(--text-primary);
	}
	.wiki-article :global(h2) {
		font-family: 'Instrument Sans', sans-serif;
		font-size: 1.25rem;
		font-weight: 600;
		margin: 1.25rem 0 0.5rem;
		color: var(--text-primary);
	}
	.wiki-article :global(h3) {
		font-family: 'Instrument Sans', sans-serif;
		font-size: 1.1rem;
		font-weight: 600;
		margin: 1rem 0 0.5rem;
		color: var(--text-primary);
	}
	.wiki-article :global(p) {
		margin: 0.5rem 0;
	}
	.wiki-article :global(ul),
	.wiki-article :global(ol) {
		padding-left: 1.5rem;
		margin: 0.5rem 0;
	}
	.wiki-article :global(ul) {
		list-style-type: disc;
	}
	.wiki-article :global(ol) {
		list-style-type: decimal;
	}
	.wiki-article :global(li) {
		margin: 0.15rem 0;
	}
	.wiki-article :global(.wiki-inline-code) {
		font-family: 'JetBrains Mono', monospace;
		font-size: 0.85em;
		background: rgba(59, 130, 246, 0.1);
		padding: 2px 6px;
		border-radius: 4px;
		color: var(--accent-blue);
	}
	.wiki-article :global(.wiki-code-block) {
		font-family: 'JetBrains Mono', monospace;
		font-size: 0.8em;
		background: #0d1117;
		padding: 1rem;
		border-radius: 8px;
		overflow-x: auto;
		margin: 1rem 0;
		white-space: pre;
		line-height: 1.5;
		border: 1px solid var(--border);
	}
	.wiki-article :global(.wiki-code-block code) {
		background: none;
		padding: 0;
		color: var(--text-primary);
	}
	.wiki-article :global(.wiki-ext-link),
	.wiki-article :global(a:not(.wiki-link)) {
		color: #3b82f6;
		text-decoration: none;
	}
	.wiki-article :global(.wiki-ext-link:hover),
	.wiki-article :global(a:not(.wiki-link):hover) {
		text-decoration: underline;
	}
	.wiki-article :global(.wiki-link) {
		color: #3b82f6;
		text-decoration: underline;
	}
	.wiki-article :global(.wiki-link:hover) {
		color: #60a5fa;
	}
	.wiki-article :global(.wiki-link.wanted) {
		color: #ef4444;
		text-decoration: underline dashed;
	}
	.wiki-article :global(.wiki-link.wanted:hover) {
		color: #f87171;
	}
	.wiki-article :global(.wiki-blockquote) {
		border-left: 3px solid #3b82f6;
		padding-left: 1rem;
		color: #94a3b8;
		margin: 0.5rem 0;
	}
	.wiki-article :global(strong) {
		font-weight: 700;
	}
	.wiki-article :global(em) {
		font-style: italic;
	}
</style>

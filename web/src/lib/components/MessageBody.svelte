<script lang="ts">
	let { body, truncate }: { body: string; truncate?: number } = $props();

	/**
	 * Lightweight markdown renderer with @mentions, #channels, and URL auto-linking.
	 * No external dependencies. Input is sanitized (HTML stripped) before processing.
	 */

	// Strip all HTML tags from raw input to prevent XSS
	function stripHtml(text: string): string {
		return text.replace(/<[^>]*>/g, '');
	}

	// Escape HTML special characters in text content
	function escapeHtml(text: string): string {
		return text
			.replace(/&/g, '&amp;')
			.replace(/</g, '&lt;')
			.replace(/>/g, '&gt;')
			.replace(/"/g, '&quot;');
	}

	// Process inline markdown: bold, italic, code, URLs, @mentions, #channels
	function processInline(text: string): string {
		// Inline code first (so content inside is not further processed)
		let result = '';
		const codeRegex = /`([^`]+)`/g;
		let lastIndex = 0;
		let match;

		while ((match = codeRegex.exec(text)) !== null) {
			result += processInlineNonCode(text.slice(lastIndex, match.index));
			result += `<code class="msg-inline-code">${escapeHtml(match[1])}</code>`;
			lastIndex = codeRegex.lastIndex;
		}
		result += processInlineNonCode(text.slice(lastIndex));
		return result;
	}

	// Process inline elements that are NOT inside code spans
	function processInlineNonCode(text: string): string {
		// Extract URLs BEFORE HTML escaping to avoid breaking them
		const urlPlaceholders: string[] = [];
		let s = text.replace(
			/https?:\/\/[^\s<>()[\]"'`]+/g,
			(url) => {
				// Strip trailing punctuation that's likely not part of the URL
				const cleaned = url.replace(/[.,;:!?)]+$/, '');
				const trailing = url.slice(cleaned.length);
				const idx = urlPlaceholders.length;
				urlPlaceholders.push(
					`<a href="${escapeHtml(cleaned)}" target="_blank" rel="noopener" class="msg-link">${escapeHtml(cleaned)}</a>${escapeHtml(trailing)}`
				);
				return `\x01URL${idx}\x01`;
			}
		);

		// Now HTML-escape the rest
		s = escapeHtml(s);

		// Bold: **text**
		s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');

		// Italic: *text* (but not inside **)
		s = s.replace(/(?<!\*)\*([^*]+)\*(?!\*)/g, '<em>$1</em>');

		// @mentions
		s = s.replace(
			/@([\w][\w.-]*)/g,
			'<a href="/dm/$1" class="msg-mention">@$1</a>'
		);

		// #channels
		s = s.replace(
			/#([\w][\w.-]*)/g,
			'<a href="/channels/$1" class="msg-channel">$&</a>'
		);

		// Restore URL placeholders
		s = s.replace(/\x01URL(\d+)\x01/g, (_, idx) => urlPlaceholders[parseInt(idx)]);

		return s;
	}

	// Parse a full message body into HTML
	function renderMarkdown(raw: string): string {
		const sanitized = stripHtml(raw);

		// Handle truncation on the sanitized text
		let text = sanitized;
		if (truncate && text.length > truncate) {
			text = text.slice(0, truncate) + '...';
		}

		// Fenced code blocks: ```...```
		const blocks: string[] = [];
		const codeBlockRegex = /```(?:\w*)\n?([\s\S]*?)```/g;
		let processed = text.replace(codeBlockRegex, (_match, code) => {
			const idx = blocks.length;
			blocks.push(`<pre class="msg-code-block"><code>${escapeHtml(code.replace(/\n$/, ''))}</code></pre>`);
			return `\x00BLOCK${idx}\x00`;
		});

		// Split into paragraphs by blank lines
		const paragraphs = processed.split(/\n{2,}/);
		const htmlParts: string[] = [];

		for (const para of paragraphs) {
			// Check for block placeholder
			if (/^\x00BLOCK\d+\x00$/.test(para.trim())) {
				const idx = parseInt(para.trim().replace(/\x00BLOCK(\d+)\x00/, '$1'));
				htmlParts.push(blocks[idx]);
				continue;
			}

			const lines = para.split('\n');

			// Check if this paragraph is a list
			const isUnordered = lines.every(l => /^\s*[-*]\s/.test(l) || l.trim() === '');
			const isOrdered = lines.every(l => /^\s*\d+\.\s/.test(l) || l.trim() === '');

			if (isUnordered && lines.some(l => l.trim() !== '')) {
				const items = lines
					.filter(l => /^\s*[-*]\s/.test(l))
					.map(l => `<li>${processInline(l.replace(/^\s*[-*]\s+/, ''))}</li>`)
					.join('');
				htmlParts.push(`<ul class="msg-list">${items}</ul>`);
			} else if (isOrdered && lines.some(l => l.trim() !== '')) {
				const items = lines
					.filter(l => /^\s*\d+\.\s/.test(l))
					.map(l => `<li>${processInline(l.replace(/^\s*\d+\.\s+/, ''))}</li>`)
					.join('');
				htmlParts.push(`<ol class="msg-list msg-list-ordered">${items}</ol>`);
			} else {
				// Check for headers
				const headerMatch = para.match(/^(#{1,6})\s+(.+)$/);
				if (headerMatch) {
					const level = headerMatch[1].length;
					htmlParts.push(`<h${level} class="msg-heading msg-h${level}">${processInline(headerMatch[2])}</h${level}>`);
				} else {
					// Regular paragraph - preserve line breaks within
					const inlineHtml = lines
						.map(l => {
							// Replace block placeholders inline
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

	let rendered = $derived(renderMarkdown(body));
</script>

<div class="msg-body">
	{@html rendered}
</div>

<style>
	.msg-body {
		/* Inherit parent text styling */
	}
	.msg-body :global(p) {
		margin: 0;
	}
	.msg-body :global(p + p) {
		margin-top: 0.5em;
	}
	.msg-body :global(strong) {
		font-weight: 700;
	}
	.msg-body :global(em) {
		font-style: italic;
	}
	.msg-body :global(.msg-inline-code) {
		font-family: 'JetBrains Mono', monospace;
		font-size: 0.85em;
		padding: 0.15em 0.35em;
		border-radius: 4px;
		background-color: var(--bg-tertiary);
		color: var(--accent-red);
	}
	.msg-body :global(.msg-code-block) {
		font-family: 'JetBrains Mono', monospace;
		font-size: 0.8em;
		padding: 0.75em 1em;
		border-radius: 6px;
		background-color: var(--bg-primary);
		border: 1px solid var(--border);
		overflow-x: auto;
		margin: 0.5em 0;
		white-space: pre;
		line-height: 1.5;
	}
	.msg-body :global(.msg-code-block code) {
		background: none;
		padding: 0;
		color: var(--text-primary);
	}
	.msg-body :global(.msg-link) {
		color: var(--text-link);
		text-decoration: none;
	}
	.msg-body :global(.msg-link:hover) {
		text-decoration: underline;
	}
	.msg-body :global(.msg-mention) {
		display: inline;
		padding: 0.1em 0.4em;
		border-radius: 4px;
		background-color: rgba(124, 58, 237, 0.15);
		color: var(--accent-purple);
		font-weight: 600;
		font-size: 0.92em;
		text-decoration: none;
	}
	.msg-body :global(.msg-mention:hover) {
		background-color: rgba(124, 58, 237, 0.25);
		text-decoration: none;
	}
	.msg-body :global(.msg-channel) {
		display: inline;
		padding: 0.1em 0.4em;
		border-radius: 4px;
		background-color: rgba(54, 197, 240, 0.15);
		color: var(--accent-blue);
		font-weight: 600;
		font-size: 0.92em;
		text-decoration: none;
	}
	.msg-body :global(.msg-channel:hover) {
		background-color: rgba(54, 197, 240, 0.25);
		text-decoration: none;
	}
	.msg-body :global(.msg-list) {
		margin: 0.25em 0;
		padding-left: 1.5em;
		list-style-type: disc;
	}
	.msg-body :global(.msg-list-ordered) {
		list-style-type: decimal;
	}
	.msg-body :global(.msg-list li) {
		margin: 0.1em 0;
	}
	.msg-body :global(.msg-heading) {
		font-family: 'Instrument Sans', sans-serif;
		font-weight: 700;
		margin: 0.4em 0 0.2em;
	}
	.msg-body :global(.msg-h1) {
		font-size: 1.3em;
	}
	.msg-body :global(.msg-h2) {
		font-size: 1.15em;
	}
	.msg-body :global(.msg-h3) {
		font-size: 1.05em;
	}
	.msg-body :global(.msg-h4),
	.msg-body :global(.msg-h5),
	.msg-body :global(.msg-h6) {
		font-size: 1em;
	}
</style>

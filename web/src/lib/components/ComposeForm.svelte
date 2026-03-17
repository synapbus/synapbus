<script lang="ts">
	import { messages as messagesApi, agents as agentsApi, channels as channelsApi, attachments as attachmentsApi } from '$lib/api/client';

	let { onSent = () => {} }: { onSent?: () => void } = $props();

	let to = $state('');
	let body = $state('');
	let priority = $state(5);
	let subject = $state('');
	let channelId = $state<number | undefined>(undefined);
	let sending = $state(false);
	let error = $state('');
	let showOptions = $state(false);

	// Attachment state
	type UploadedAttachment = { hash: string; original_filename: string; size: number; mime_type: string };
	let uploadedAttachments = $state<UploadedAttachment[]>([]);
	let uploading = $state(false);
	let uploadError = $state('');
	let fileInputEl: HTMLInputElement | undefined = $state(undefined);

	const ACCEPTED_FILES = '.jpg,.jpeg,.png,.gif,.webp,.svg,.pdf,.txt,.md,.csv,.json,.xml,.yaml,.yml,.log';

	function formatFileSize(bytes: number): string {
		if (bytes < 1024 * 1024) {
			return (bytes / 1024).toFixed(1) + ' KB';
		}
		return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
	}

	function triggerFileInput() {
		fileInputEl?.click();
	}

	async function handleFileSelected(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;

		uploading = true;
		uploadError = '';
		try {
			const result = await attachmentsApi.upload(file);
			uploadedAttachments = [...uploadedAttachments, result];
		} catch (err: any) {
			uploadError = err.message || 'Upload failed';
		} finally {
			uploading = false;
			// Reset file input so same file can be re-selected
			if (fileInputEl) fileInputEl.value = '';
		}
	}

	function removeAttachment(hash: string) {
		uploadedAttachments = uploadedAttachments.filter(a => a.hash !== hash);
	}

	// Autocomplete state
	let agentList = $state<any[]>([]);
	let channelList = $state<any[]>([]);
	let showSuggestions = $state(false);
	let filteredAgents = $derived(
		to.trim()
			? agentList.filter((a) =>
				a.name.toLowerCase().includes(to.toLowerCase()) ||
				(a.display_name && a.display_name.toLowerCase().includes(to.toLowerCase()))
			).slice(0, 5)
			: []
	);

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			loadOptions();
		}
	});

	async function loadOptions() {
		try {
			const [agRes, chRes] = await Promise.all([
				agentsApi.list(),
				channelsApi.list()
			]);
			agentList = agRes.agents ?? [];
			channelList = chRes.channels ?? [];
		} catch {
			// handled
		}
	}

	function selectAgent(name: string) {
		to = name;
		showSuggestions = false;
	}

	let textareaEl: HTMLTextAreaElement | undefined = $state(undefined);

	function autoResize() {
		if (!textareaEl) return;
		// Reset to measure true scrollHeight
		textareaEl.style.height = '80px';
		const maxHeight = 240; // ~12 lines
		const scrollHeight = textareaEl.scrollHeight;
		if (scrollHeight > maxHeight) {
			textareaEl.style.height = maxHeight + 'px';
			textareaEl.style.overflowY = 'auto';
			textareaEl.classList.remove('overflow-hidden');
		} else {
			textareaEl.style.height = Math.max(80, scrollHeight) + 'px';
			textareaEl.style.overflowY = 'hidden';
			textareaEl.classList.add('overflow-hidden');
		}
	}

	function isMobile(): boolean {
		return typeof window !== 'undefined' && window.innerWidth < 768;
	}

	function handleKeydown(e: KeyboardEvent) {
		// On mobile, Enter inserts newline (send via button only)
		// On desktop, Enter sends, Shift+Enter inserts newline
		if (e.key === 'Enter' && !e.shiftKey && !isMobile()) {
			e.preventDefault();
			handleSubmit();
		}
	}

	async function handleSubmit() {
		error = '';

		if (!body.trim()) {
			error = 'Message body is required';
			return;
		}
		if (!to.trim()) {
			error = 'Recipient is required';
			return;
		}

		sending = true;
		try {
			await messagesApi.send({
				to: to.trim(),
				body: body.trim(),
				priority,
				subject: subject.trim() || undefined,
				channel_id: channelId,
				attachments: uploadedAttachments.length > 0 ? uploadedAttachments.map(a => a.hash) : undefined
			});
			to = '';
			body = '';
			priority = 5;
			subject = '';
			channelId = undefined;
			uploadedAttachments = [];
			uploadError = '';
			if (textareaEl) {
				textareaEl.style.height = '72px';
				textareaEl.style.overflowY = 'hidden';
			}
			onSent();
		} catch (err: any) {
			error = err.message || 'Failed to send message';
		} finally {
			sending = false;
		}
	}

	const priorityLabels: Record<number, string> = {
		1: 'Low',
		5: 'Normal',
		8: 'High',
		10: 'Urgent'
	};
</script>

<div class="bg-bg-secondary border border-border rounded-lg overflow-hidden">
	{#if error}
		<div class="px-4 py-2 bg-accent-red/10 border-b border-accent-red/20 text-xs text-accent-red">
			{error}
		</div>
	{/if}

	<!-- To field -->
	<div class="relative border-b border-border">
		<div class="flex items-center px-4 py-2 gap-2">
			<span class="text-xs text-text-secondary font-medium flex-shrink-0">To:</span>
			<input
				type="text"
				placeholder="Agent name..."
				class="flex-1 bg-transparent text-sm text-text-primary placeholder-text-secondary outline-none"
				bind:value={to}
				onfocus={() => (showSuggestions = true)}
				onblur={() => setTimeout(() => (showSuggestions = false), 200)}
			/>
		</div>
		{#if showSuggestions && filteredAgents.length > 0}
			<div class="absolute left-0 right-0 top-full z-10 bg-bg-secondary border border-border rounded-b-lg shadow-lg max-h-40 overflow-y-auto">
				{#each filteredAgents as agent}
					<button
						class="w-full flex items-center gap-2 px-4 py-2 text-sm hover:bg-bg-tertiary transition-colors text-left"
						onmousedown={() => selectAgent(agent.name)}
					>
						<span class="w-5 h-5 rounded-full bg-bg-tertiary flex items-center justify-center text-[10px] font-bold text-text-secondary">
							{(agent.display_name || agent.name).charAt(0).toUpperCase()}
						</span>
						<span class="text-text-primary">{agent.display_name || agent.name}</span>
						<span class="text-xs text-text-secondary font-mono">@{agent.name}</span>
					</button>
				{/each}
			</div>
		{/if}
	</div>

	<!-- Message body -->
	<textarea
		bind:this={textareaEl}
		placeholder="Write a message... {isMobile() ? '' : '(Shift+Enter for new line)'}"
		class="w-full px-4 py-3 bg-transparent text-sm text-text-primary placeholder-text-secondary resize-none outline-none overflow-hidden"
		style="min-height: 80px; max-height: 240px;"
		bind:value={body}
		rows="3"
		onkeydown={handleKeydown}
		oninput={autoResize}
	></textarea>

	<!-- Options row -->
	{#if showOptions}
		<div class="px-4 py-2 border-t border-border space-y-2">
			<input
				type="text"
				placeholder="Subject (optional)"
				class="input text-xs"
				bind:value={subject}
			/>
			{#if channelList.length > 0}
				<div class="flex items-center gap-2">
					<span class="text-xs text-text-secondary">Channel:</span>
					<select class="input text-xs flex-1" bind:value={channelId}>
						<option value={undefined}>None (DM)</option>
						{#each channelList as ch}
							<option value={ch.id}>#{ch.name}</option>
						{/each}
					</select>
				</div>
			{/if}
			<div class="flex items-center gap-3">
				<span class="text-xs text-text-secondary">Priority:</span>
				<select class="input text-xs w-28" bind:value={priority}>
					<option value={1}>Low (1)</option>
					<option value={5}>Normal (5)</option>
					<option value={8}>High (8)</option>
					<option value={10}>Urgent (10)</option>
				</select>
			</div>
		</div>
	{/if}

	<!-- Hidden file input -->
	<input
		bind:this={fileInputEl}
		type="file"
		accept={ACCEPTED_FILES}
		class="hidden"
		onchange={handleFileSelected}
	/>

	<!-- Attachment list -->
	{#if uploadedAttachments.length > 0 || uploading || uploadError}
		<div class="px-4 py-2 border-t border-border space-y-1">
			{#if uploadError}
				<div class="text-xs text-accent-red">{uploadError}</div>
			{/if}
			{#if uploading}
				<div class="flex items-center gap-2 text-xs text-text-secondary">
					<svg class="w-3.5 h-3.5 animate-spin" fill="none" viewBox="0 0 24 24">
						<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
						<path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
					</svg>
					Uploading...
				</div>
			{/if}
			{#each uploadedAttachments as att (att.hash)}
				<div class="flex items-center gap-2 text-xs text-text-primary bg-bg-tertiary rounded px-2 py-1">
					<svg class="w-3.5 h-3.5 text-text-secondary flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
						<path stroke-linecap="round" stroke-linejoin="round" d="M18.375 12.739l-7.693 7.693a4.5 4.5 0 01-6.364-6.364l10.94-10.94A3 3 0 1119.5 7.372L8.552 18.32m.009-.01l-.01.01m5.699-9.941l-7.81 7.81a1.5 1.5 0 002.112 2.13" />
					</svg>
					<span class="truncate flex-1">{att.original_filename}</span>
					<span class="text-text-secondary flex-shrink-0">{formatFileSize(att.size)}</span>
					<button
						class="p-0.5 rounded hover:bg-bg-secondary text-text-secondary hover:text-accent-red transition-colors flex-shrink-0"
						onclick={() => removeAttachment(att.hash)}
						title="Remove attachment"
					>
						<svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
							<path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
						</svg>
					</button>
				</div>
			{/each}
		</div>
	{/if}

	<!-- Bottom bar -->
	<div class="flex items-center justify-between px-3 py-2 border-t border-border">
		<div class="flex items-center gap-1">
			<button
				class="text-xs text-text-secondary hover:text-text-primary transition-colors px-2 py-1 rounded hover:bg-bg-tertiary"
				onclick={() => (showOptions = !showOptions)}
			>
				{showOptions ? 'Hide options' : 'Options'}
			</button>
			<button
				class="p-1.5 rounded text-text-secondary hover:text-text-primary hover:bg-bg-tertiary transition-colors disabled:opacity-50"
				onclick={triggerFileInput}
				disabled={uploading}
				title="Attach file"
			>
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
					<path stroke-linecap="round" stroke-linejoin="round" d="M18.375 12.739l-7.693 7.693a4.5 4.5 0 01-6.364-6.364l10.94-10.94A3 3 0 1119.5 7.372L8.552 18.32m.009-.01l-.01.01m5.699-9.941l-7.81 7.81a1.5 1.5 0 002.112 2.13" />
				</svg>
			</button>
		</div>
		<button
			class="px-4 py-1.5 bg-accent-green rounded text-xs font-semibold text-white hover:brightness-110 transition-all disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-1.5"
			disabled={sending || !body.trim() || !to.trim()}
			onclick={handleSubmit}
		>
			{#if sending}
				Sending...
			{:else}
				<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
					<path stroke-linecap="round" stroke-linejoin="round" d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" />
				</svg>
				Send
			{/if}
		</button>
	</div>
</div>

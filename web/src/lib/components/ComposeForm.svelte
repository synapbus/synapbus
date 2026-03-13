<script lang="ts">
	import { messages as messagesApi, agents as agentsApi, channels as channelsApi } from '$lib/api/client';

	let { onSent = () => {} }: { onSent?: () => void } = $props();

	let to = $state('');
	let body = $state('');
	let priority = $state(5);
	let subject = $state('');
	let channelId = $state<number | undefined>(undefined);
	let sending = $state(false);
	let error = $state('');
	let showOptions = $state(false);

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

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
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
				channel_id: channelId
			});
			to = '';
			body = '';
			priority = 5;
			subject = '';
			channelId = undefined;
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
		placeholder="Write a message..."
		class="w-full px-4 py-3 bg-transparent text-sm text-text-primary placeholder-text-secondary resize-none outline-none min-h-[80px]"
		bind:value={body}
		rows="3"
		onkeydown={handleKeydown}
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

	<!-- Bottom bar -->
	<div class="flex items-center justify-between px-3 py-2 border-t border-border">
		<button
			class="text-xs text-text-secondary hover:text-text-primary transition-colors px-2 py-1 rounded hover:bg-bg-tertiary"
			onclick={() => (showOptions = !showOptions)}
		>
			{showOptions ? 'Hide options' : 'Options'}
		</button>
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

<script lang="ts">
	import { channels as channelsApi } from '$lib/api/client';

	let channelList = $state<any[]>([]);
	let loadingData = $state(true);
	let showCreate = $state(false);

	let newName = $state('');
	let newDescription = $state('');
	let newIsPrivate = $state(false);
	let newType = $state('standard');
	let newWorkflowEnabled = $state(false);
	let creating = $state(false);
	let createError = $state('');

	async function loadChannels() {
		loadingData = true;
		try {
			const res = await channelsApi.list();
			channelList = res.channels;
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
			loadChannels();
		}
	});

	async function handleCreate(e: SubmitEvent) {
		e.preventDefault();
		if (!newName.trim()) {
			createError = 'Channel name is required';
			return;
		}
		creating = true;
		createError = '';
		try {
			await channelsApi.create({
				name: newName.trim(),
				description: newDescription.trim(),
				is_private: newIsPrivate,
				type: newType
			});
			// Enable workflow if requested (separate settings call)
			if (newWorkflowEnabled) {
				try {
					await channelsApi.updateSettings(newName.trim(), { workflow_enabled: true });
				} catch {
					// Channel created but workflow toggle failed -- non-fatal
				}
			}
			newName = '';
			newDescription = '';
			newIsPrivate = false;
			newType = 'standard';
			newWorkflowEnabled = false;
			showCreate = false;
			await loadChannels();
		} catch (err: any) {
			createError = err.message || 'Failed to create channel';
		} finally {
			creating = false;
		}
	}
</script>

<div class="p-5 max-w-5xl">
	<div class="flex items-center justify-between mb-5">
		<h1 class="text-xl font-bold text-text-primary font-display">Channels</h1>
		<button class="btn-primary flex items-center gap-1.5" onclick={() => (showCreate = !showCreate)}>
			{#if showCreate}
				Cancel
			{:else}
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
					<path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
				</svg>
				Create Channel
			{/if}
		</button>
	</div>

	{#if showCreate}
		<form class="card p-5 mb-5" onsubmit={handleCreate}>
			{#if createError}
				<div class="mb-3 px-3 py-2 bg-accent-red/10 rounded text-xs text-accent-red">{createError}</div>
			{/if}
			<div class="space-y-3">
				<input type="text" class="input" placeholder="Channel name (e.g. research-findings)" bind:value={newName} />
				<input type="text" class="input" placeholder="Description (optional)" bind:value={newDescription} />
				<div>
					<label class="block text-xs text-text-secondary mb-1" for="channel-type">Channel Type</label>
					<select id="channel-type" bind:value={newType} class="input">
						<option value="standard">Standard</option>
						<option value="blackboard">Blackboard</option>
						<option value="auction">Auction</option>
					</select>
				</div>
				<label class="flex items-center gap-2 text-sm text-text-secondary cursor-pointer">
					<input type="checkbox" bind:checked={newIsPrivate} class="rounded bg-bg-input border-border text-accent-green focus:ring-accent-green" />
					Private channel (invite-only)
				</label>
				{#if newType !== 'auction'}
					<label class="flex items-center gap-2 text-sm text-text-secondary cursor-pointer">
						<input type="checkbox" bind:checked={newWorkflowEnabled} class="rounded bg-bg-input border-border text-accent-green focus:ring-accent-green" />
						Enable workflow reactions
					</label>
				{/if}
				<button type="submit" class="btn-primary" disabled={creating}>
					{creating ? 'Creating...' : 'Create'}
				</button>
			</div>
		</form>
	{/if}

	<div class="card">
		{#if loadingData}
			<div class="p-5 space-y-3">
				{#each Array(3) as _}
					<div class="skeleton h-12 w-full"></div>
				{/each}
			</div>
		{:else if channelList.length === 0}
			<div class="p-8 text-center text-text-secondary text-sm">
				No channels yet. Create one to get started.
			</div>
		{:else}
			<div class="divide-y divide-border">
				{#each channelList as ch}
					<a href="/channels/{ch.name}" class="block px-5 py-3 hover:bg-bg-tertiary/50 transition-colors">
						<div class="flex items-center justify-between">
							<div class="min-w-0">
								<div class="flex items-center gap-2">
									<span class="font-medium text-sm text-text-primary font-mono">#{ch.name}</span>
									{#if ch.is_private}
										<svg class="w-3.5 h-3.5 text-text-secondary" fill="currentColor" viewBox="0 0 20 20">
											<path fill-rule="evenodd" d="M5 9V7a5 5 0 0110 0v2a2 2 0 012 2v5a2 2 0 01-2 2H5a2 2 0 01-2-2v-5a2 2 0 012-2zm8-2v2H7V7a3 3 0 016 0z" clip-rule="evenodd" />
										</svg>
									{/if}
								</div>
								{#if ch.description}
									<p class="text-xs text-text-secondary truncate mt-0.5">{ch.description}</p>
								{/if}
							</div>
							<div class="flex items-center gap-2 flex-shrink-0 ml-3">
								{#if ch.type && ch.type !== 'standard'}
									<span class="badge bg-accent-purple/20 text-accent-purple text-[10px]">{ch.type}</span>
								{/if}
								<span class="badge bg-bg-tertiary text-text-secondary">
									{ch.member_count} members
								</span>
							</div>
						</div>
					</a>
				{/each}
			</div>
		{/if}
	</div>
</div>

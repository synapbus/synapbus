<script lang="ts">
	import { onMount } from 'svelte';
	import { channels as channelsApi } from '$lib/api/client';

	let channelList = $state<any[]>([]);
	let loadingData = $state(true);
	let showCreate = $state(false);

	// Create form
	let newName = $state('');
	let newDescription = $state('');
	let newIsPrivate = $state(false);
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

	onMount(loadChannels);

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
				is_private: newIsPrivate
			});
			newName = '';
			newDescription = '';
			newIsPrivate = false;
			showCreate = false;
			await loadChannels();
		} catch (err: any) {
			createError = err.message || 'Failed to create channel';
		} finally {
			creating = false;
		}
	}
</script>

<div class="max-w-4xl mx-auto">
	<div class="flex items-center justify-between mb-6">
		<h1 class="text-2xl font-bold text-gray-900 dark:text-white">Channels</h1>
		<button class="btn-primary" onclick={() => (showCreate = !showCreate)}>
			{showCreate ? 'Cancel' : 'Create Channel'}
		</button>
	</div>

	{#if showCreate}
		<form class="card p-4 mb-6" onsubmit={handleCreate}>
			{#if createError}
				<div class="mb-3 p-2 bg-red-50 dark:bg-red-900/30 rounded text-sm text-red-700 dark:text-red-300">{createError}</div>
			{/if}
			<div class="space-y-3">
				<input type="text" class="input" placeholder="Channel name (e.g. research-findings)" bind:value={newName} />
				<input type="text" class="input" placeholder="Description (optional)" bind:value={newDescription} />
				<label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
					<input type="checkbox" bind:checked={newIsPrivate} class="rounded" />
					Private channel (invite-only)
				</label>
				<button type="submit" class="btn-primary" disabled={creating}>
					{creating ? 'Creating...' : 'Create'}
				</button>
			</div>
		</form>
	{/if}

	<div class="card">
		{#if loadingData}
			<div class="p-4 space-y-3">
				{#each Array(3) as _}
					<div class="skeleton h-12 w-full"></div>
				{/each}
			</div>
		{:else if channelList.length === 0}
			<div class="p-8 text-center text-gray-500 dark:text-gray-400">
				<p>No channels yet. Create one to get started.</p>
			</div>
		{:else}
			<div class="divide-y divide-gray-100 dark:divide-gray-700">
				{#each channelList as ch}
					<a href="/channels/{ch.name}" class="block px-4 py-3 hover:bg-gray-50 dark:hover:bg-gray-700/50 transition-colors">
						<div class="flex items-center justify-between">
							<div class="min-w-0">
								<div class="flex items-center gap-2">
									<span class="font-medium text-sm text-gray-900 dark:text-gray-100">#{ch.name}</span>
									{#if ch.is_private}
										<svg class="w-3.5 h-3.5 text-gray-400" fill="currentColor" viewBox="0 0 20 20">
											<path fill-rule="evenodd" d="M5 9V7a5 5 0 0110 0v2a2 2 0 012 2v5a2 2 0 01-2 2H5a2 2 0 01-2-2v-5a2 2 0 012-2zm8-2v2H7V7a3 3 0 016 0z" clip-rule="evenodd" />
										</svg>
									{/if}
								</div>
								{#if ch.description}
									<p class="text-xs text-gray-500 dark:text-gray-400 truncate mt-0.5">{ch.description}</p>
								{/if}
							</div>
							<span class="badge bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-400 flex-shrink-0 ml-3">
								{ch.member_count} members
							</span>
						</div>
					</a>
				{/each}
			</div>
		{/if}
	</div>
</div>

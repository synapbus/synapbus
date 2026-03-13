<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { channels as channelsApi } from '$lib/api/client';

	let channel = $state<any>(null);
	let members = $state<any[]>([]);
	let loadingData = $state(true);
	let joinError = $state('');
	let joining = $state(false);

	$: channelName = $page.params.name;

	async function loadChannel() {
		loadingData = true;
		try {
			const res = await channelsApi.get(channelName);
			channel = res.channel;
			members = res.members;
		} catch {
			// handled
		} finally {
			loadingData = false;
		}
	}

	onMount(loadChannel);

	async function handleJoin() {
		joining = true;
		joinError = '';
		try {
			await channelsApi.join(channelName);
			await loadChannel();
		} catch (err: any) {
			joinError = err.message || 'Failed to join channel';
		} finally {
			joining = false;
		}
	}

	async function handleLeave() {
		joining = true;
		joinError = '';
		try {
			await channelsApi.leave(channelName);
			await loadChannel();
		} catch (err: any) {
			joinError = err.message || 'Failed to leave channel';
		} finally {
			joining = false;
		}
	}
</script>

<div class="max-w-4xl mx-auto">
	<a href="/channels" class="text-sm text-primary-600 hover:text-primary-700 dark:text-primary-400 mb-4 inline-block">
		&larr; Back to channels
	</a>

	{#if loadingData}
		<div class="card p-6">
			<div class="skeleton h-6 w-1/4 mb-2"></div>
			<div class="skeleton h-4 w-1/2 mb-4"></div>
			<div class="skeleton h-20 w-full"></div>
		</div>
	{:else if channel}
		<div class="card mb-6">
			<div class="px-4 py-4 border-b border-gray-200 dark:border-gray-700">
				<div class="flex items-center justify-between">
					<div>
						<div class="flex items-center gap-2">
							<h1 class="text-xl font-bold text-gray-900 dark:text-white">#{channel.name}</h1>
							{#if channel.is_private}
								<span class="badge bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-400">Private</span>
							{/if}
							<span class="badge bg-blue-100 text-blue-700 dark:bg-blue-900 dark:text-blue-300">{channel.type}</span>
						</div>
						{#if channel.description}
							<p class="text-sm text-gray-500 dark:text-gray-400 mt-1">{channel.description}</p>
						{/if}
						{#if channel.topic}
							<p class="text-xs text-gray-400 dark:text-gray-500 mt-1">Topic: {channel.topic}</p>
						{/if}
					</div>
					<div class="flex gap-2">
						<button class="btn-primary text-sm" onclick={handleJoin} disabled={joining}>
							{joining ? '...' : 'Join'}
						</button>
						<button class="btn-secondary text-sm" onclick={handleLeave} disabled={joining}>
							Leave
						</button>
					</div>
				</div>
				{#if joinError}
					<div class="mt-2 text-sm text-red-600 dark:text-red-400">{joinError}</div>
				{/if}
			</div>
		</div>

		<!-- Members -->
		<div class="card">
			<div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
				<h2 class="font-semibold text-gray-900 dark:text-gray-100">Members ({members.length})</h2>
			</div>
			{#if members.length === 0}
				<div class="p-6 text-center text-gray-500 dark:text-gray-400">No members</div>
			{:else}
				<div class="divide-y divide-gray-100 dark:divide-gray-700">
					{#each members as member}
						<div class="px-4 py-2 flex items-center justify-between">
							<span class="text-sm text-gray-900 dark:text-gray-100">{member.agent_name}</span>
							<span class="badge {member.role === 'owner' ? 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900 dark:text-yellow-300' : 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-400'}">
								{member.role}
							</span>
						</div>
					{/each}
				</div>
			{/if}
		</div>
	{:else}
		<div class="card p-8 text-center">
			<p class="text-gray-500 dark:text-gray-400">Channel not found</p>
		</div>
	{/if}
</div>

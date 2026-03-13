<script lang="ts">
	import { page } from '$app/stores';
	import { channels as channelsApi } from '$lib/api/client';

	let channel = $state<any>(null);
	let members = $state<any[]>([]);
	let loadingData = $state(true);
	let joinError = $state('');
	let joining = $state(false);

	let channelName = $derived($page.params.name);

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

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			loadChannel();
		}
	});

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

<div class="p-5 max-w-5xl">
	<a href="/channels" class="inline-flex items-center gap-1 text-xs text-text-link hover:underline mb-4">
		<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
			<path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
		</svg>
		Back to channels
	</a>

	{#if loadingData}
		<div class="card p-6">
			<div class="skeleton h-6 w-1/4 mb-2"></div>
			<div class="skeleton h-4 w-1/2 mb-4"></div>
			<div class="skeleton h-20 w-full"></div>
		</div>
	{:else if channel}
		<div class="card mb-5">
			<div class="px-5 py-4 border-b border-border">
				<div class="flex items-center justify-between">
					<div>
						<div class="flex items-center gap-2">
							<h1 class="text-lg font-bold text-text-primary font-display font-mono">#{channel.name}</h1>
							{#if channel.is_private}
								<span class="badge bg-bg-tertiary text-text-secondary">Private</span>
							{/if}
							<span class="badge bg-accent-blue/20 text-accent-blue">{channel.type}</span>
						</div>
						{#if channel.description}
							<p class="text-sm text-text-secondary mt-1">{channel.description}</p>
						{/if}
						{#if channel.topic}
							<p class="text-xs text-text-secondary mt-1">Topic: {channel.topic}</p>
						{/if}
					</div>
					<div class="flex gap-2">
						<button class="btn-primary text-xs" onclick={handleJoin} disabled={joining}>
							{joining ? '...' : 'Join'}
						</button>
						<button class="btn-secondary text-xs" onclick={handleLeave} disabled={joining}>
							Leave
						</button>
					</div>
				</div>
				{#if joinError}
					<div class="mt-2 text-xs text-accent-red">{joinError}</div>
				{/if}
			</div>
		</div>

		<!-- Members -->
		<div class="card">
			<div class="px-5 py-3 border-b border-border">
				<h2 class="font-semibold text-sm text-text-primary font-display">Members ({members.length})</h2>
			</div>
			{#if members.length === 0}
				<div class="p-6 text-center text-text-secondary text-sm">No members</div>
			{:else}
				<div class="divide-y divide-border">
					{#each members as member}
						<div class="px-5 py-2.5 flex items-center justify-between">
							<div class="flex items-center gap-2">
								<span class="w-6 h-6 rounded-full bg-bg-tertiary flex items-center justify-center text-[10px] font-bold text-text-secondary">
									{member.agent_name.charAt(0).toUpperCase()}
								</span>
								<span class="text-sm text-text-primary">{member.agent_name}</span>
							</div>
							<span class="badge {member.role === 'owner' ? 'bg-accent-yellow/20 text-accent-yellow' : 'bg-bg-tertiary text-text-secondary'}">
								{member.role}
							</span>
						</div>
					{/each}
				</div>
			{/if}
		</div>
	{:else}
		<div class="card p-8 text-center text-text-secondary text-sm">
			Channel not found
		</div>
	{/if}
</div>

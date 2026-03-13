<script lang="ts">
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { user, logout } from '$lib/stores/auth';
	import { channels as channelsApi, agents as agentsApi } from '$lib/api/client';

	let channelList = $state<any[]>([]);
	let agentList = $state<any[]>([]);

	let channelsExpanded = $state(true);
	let dmsExpanded = $state(true);
	let adminExpanded = $state(true);

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized && $user) {
			_initialized = true;
			loadSidebarData();
		}
	});

	async function loadSidebarData() {
		try {
			const [chRes, agRes] = await Promise.all([
				channelsApi.list(),
				agentsApi.list()
			]);
			channelList = chRes.channels ?? [];
			agentList = agRes.agents ?? [];
		} catch {
			// handled
		}
	}

	function isActive(href: string): boolean {
		return $page.url.pathname === href || ($page.url.pathname.startsWith(href) && href !== '/');
	}

	async function handleLogout() {
		await logout();
		goto('/login');
	}

	const adminLinks = [
		{ href: '/agents', label: 'Agents' },
		{ href: '/settings', label: 'Settings' },
		{ href: '/settings/api-keys', label: 'API Keys' }
	];
</script>

<aside class="fixed top-0 left-0 z-40 h-screen w-[260px] bg-bg-secondary border-r border-border flex flex-col select-none">
	<!-- Workspace header -->
	<div class="flex items-center gap-2.5 h-14 px-4 border-b border-border flex-shrink-0">
		<div class="w-7 h-7 rounded-lg bg-accent-purple flex items-center justify-center">
			<svg class="w-4 h-4 text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
				<path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" />
			</svg>
		</div>
		<div class="min-w-0 flex-1">
			<h1 class="text-sm font-bold text-text-primary font-display truncate">SynapBus</h1>
			<p class="text-[10px] text-text-secondary leading-tight">Mission Control</p>
		</div>
		<!-- User avatar -->
		<button
			class="w-7 h-7 rounded-full bg-accent-green/20 flex items-center justify-center text-accent-green text-xs font-bold hover:bg-accent-green/30 transition-colors"
			onclick={handleLogout}
			title="Logout"
		>
			{$user?.username?.charAt(0).toUpperCase() ?? '?'}
		</button>
	</div>

	<!-- Search bar -->
	<div class="px-3 py-2.5 flex-shrink-0">
		<button
			class="w-full flex items-center gap-2 px-2.5 py-1.5 bg-bg-tertiary rounded text-text-secondary text-xs hover:bg-bg-input transition-colors"
			onclick={() => goto('/conversations')}
		>
			<svg class="w-3.5 h-3.5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
				<path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
			</svg>
			Search messages
		</button>
	</div>

	<!-- Scrollable nav -->
	<nav class="flex-1 overflow-y-auto px-2 pb-3">
		<!-- Dashboard -->
		<a
			href="/"
			class="sidebar-item mb-1 {isActive('/') && $page.url.pathname === '/' ? 'sidebar-item-active' : ''}"
		>
			<svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
				<path stroke-linecap="round" stroke-linejoin="round" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6" />
			</svg>
			Dashboard
		</a>

		<!-- Conversations -->
		<a
			href="/conversations"
			class="sidebar-item mb-3 {isActive('/conversations') ? 'sidebar-item-active' : ''}"
		>
			<svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
				<path stroke-linecap="round" stroke-linejoin="round" d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z" />
			</svg>
			Conversations
		</a>

		<!-- Channels section -->
		<div class="mb-3">
			<button
				class="section-header w-full hover:text-text-primary transition-colors"
				onclick={() => (channelsExpanded = !channelsExpanded)}
			>
				<span class="flex items-center gap-1">
					<svg class="w-3 h-3 transition-transform {channelsExpanded ? 'rotate-0' : '-rotate-90'}" fill="currentColor" viewBox="0 0 20 20">
						<path fill-rule="evenodd" d="M5.293 7.293a1 1 0 011.414 0L10 10.586l3.293-3.293a1 1 0 111.414 1.414l-4 4a1 1 0 01-1.414 0l-4-4a1 1 0 010-1.414z" clip-rule="evenodd" />
					</svg>
					Channels
				</span>
				<a
					href="/channels"
					class="text-text-secondary hover:text-text-primary p-0.5"
					onclick={(e) => e.stopPropagation()}
					title="Manage channels"
				>
					<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
						<path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
					</svg>
				</a>
			</button>
			{#if channelsExpanded}
				<div class="mt-0.5">
					{#if channelList.length === 0}
						<p class="px-3 py-1 text-xs text-text-secondary italic">No channels</p>
					{:else}
						{#each channelList as ch}
							<a
								href="/channels/{ch.name}"
								class="sidebar-item {isActive('/channels/' + ch.name) ? 'sidebar-item-active' : ''}"
							>
								<span class="text-text-secondary font-mono text-xs">#</span>
								<span class="truncate">{ch.name}</span>
								{#if ch.is_private}
									<svg class="w-3 h-3 text-text-secondary ml-auto flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
										<path fill-rule="evenodd" d="M5 9V7a5 5 0 0110 0v2a2 2 0 012 2v5a2 2 0 01-2 2H5a2 2 0 01-2-2v-5a2 2 0 012-2zm8-2v2H7V7a3 3 0 016 0z" clip-rule="evenodd" />
									</svg>
								{/if}
							</a>
						{/each}
					{/if}
				</div>
			{/if}
		</div>

		<!-- Direct Messages (Agents) section -->
		<div class="mb-3">
			<button
				class="section-header w-full hover:text-text-primary transition-colors"
				onclick={() => (dmsExpanded = !dmsExpanded)}
			>
				<span class="flex items-center gap-1">
					<svg class="w-3 h-3 transition-transform {dmsExpanded ? 'rotate-0' : '-rotate-90'}" fill="currentColor" viewBox="0 0 20 20">
						<path fill-rule="evenodd" d="M5.293 7.293a1 1 0 011.414 0L10 10.586l3.293-3.293a1 1 0 111.414 1.414l-4 4a1 1 0 01-1.414 0l-4-4a1 1 0 010-1.414z" clip-rule="evenodd" />
					</svg>
					Direct Messages
				</span>
			</button>
			{#if dmsExpanded}
				<div class="mt-0.5">
					{#if agentList.length === 0}
						<p class="px-3 py-1 text-xs text-text-secondary italic">No agents</p>
					{:else}
						{#each agentList as agent}
							<a
								href="/agents/{agent.name}"
								class="sidebar-item {isActive('/agents/' + agent.name) ? 'sidebar-item-active' : ''}"
							>
								<span class="relative flex-shrink-0">
									<span class="w-5 h-5 rounded-full bg-bg-tertiary flex items-center justify-center text-[10px] font-bold text-text-secondary">
										{(agent.display_name || agent.name).charAt(0).toUpperCase()}
									</span>
									<span
										class="absolute -bottom-0.5 -right-0.5 w-2 h-2 rounded-full border border-bg-secondary {agent.status === 'active' ? 'bg-accent-green' : 'bg-text-secondary'}"
									></span>
								</span>
								<span class="truncate">{agent.display_name || agent.name}</span>
								{#if agent.type === 'ai'}
									<span class="ml-auto text-[9px] font-mono text-accent-purple bg-accent-purple/10 px-1 rounded flex-shrink-0">AI</span>
								{/if}
							</a>
						{/each}
					{/if}
				</div>
			{/if}
		</div>

		<!-- Admin section -->
		<div class="mb-3">
			<button
				class="section-header w-full hover:text-text-primary transition-colors"
				onclick={() => (adminExpanded = !adminExpanded)}
			>
				<span class="flex items-center gap-1">
					<svg class="w-3 h-3 transition-transform {adminExpanded ? 'rotate-0' : '-rotate-90'}" fill="currentColor" viewBox="0 0 20 20">
						<path fill-rule="evenodd" d="M5.293 7.293a1 1 0 011.414 0L10 10.586l3.293-3.293a1 1 0 111.414 1.414l-4 4a1 1 0 01-1.414 0l-4-4a1 1 0 010-1.414z" clip-rule="evenodd" />
					</svg>
					Admin
				</span>
			</button>
			{#if adminExpanded}
				<div class="mt-0.5">
					{#each adminLinks as link}
						<a
							href={link.href}
							class="sidebar-item {isActive(link.href) ? 'sidebar-item-active' : ''}"
						>
							{#if link.label === 'Agents'}
								<svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
									<path stroke-linecap="round" stroke-linejoin="round" d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
								</svg>
							{:else if link.label === 'Settings'}
								<svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
									<path stroke-linecap="round" stroke-linejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
									<path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
								</svg>
							{:else if link.label === 'API Keys'}
								<svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
									<path stroke-linecap="round" stroke-linejoin="round" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
								</svg>
							{/if}
							{link.label}
						</a>
					{/each}
				</div>
			{/if}
		</div>
	</nav>

	<!-- Bottom status -->
	<div class="px-3 py-2 border-t border-border flex-shrink-0">
		<div class="flex items-center gap-2 text-xs text-text-secondary">
			<span class="w-2 h-2 rounded-full bg-accent-green"></span>
			<span class="truncate">{$user?.display_name ?? $user?.username ?? 'Connected'}</span>
		</div>
	</div>
</aside>

<script lang="ts">
	import { page } from '$app/stores';

	let { open = $bindable(false) } = $props();

	const links = [
		{ href: '/', label: 'Dashboard', icon: 'M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6' },
		{ href: '/conversations', label: 'Conversations', icon: 'M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z' },
		{ href: '/channels', label: 'Channels', icon: 'M7 20l4-16m2 16l4-16M6 9h14M4 15h14' },
		{ href: '/agents', label: 'Agents', icon: 'M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z' },
		{ href: '/settings', label: 'Settings', icon: 'M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z' }
	];
</script>

<!-- Mobile overlay -->
{#if open}
	<div class="fixed inset-0 z-30 bg-black/50 lg:hidden" onclick={() => (open = false)}></div>
{/if}

<aside
	class="fixed top-0 left-0 z-40 h-screen w-64 transform bg-white dark:bg-gray-800 border-r border-gray-200 dark:border-gray-700 transition-transform lg:translate-x-0 {open ? 'translate-x-0' : '-translate-x-full'}"
>
	<div class="flex items-center gap-2 h-16 px-6 border-b border-gray-200 dark:border-gray-700">
		<svg class="w-8 h-8 text-primary-600" viewBox="0 0 24 24" fill="currentColor">
			<path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" stroke="currentColor" stroke-width="2" fill="none"/>
		</svg>
		<span class="text-xl font-bold text-gray-900 dark:text-white">SynapBus</span>
	</div>

	<nav class="px-3 py-4 space-y-1">
		{#each links as link}
			{@const active = $page.url.pathname === link.href || (link.href !== '/' && $page.url.pathname.startsWith(link.href))}
			<a
				href={link.href}
				class="flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-colors {active
					? 'bg-primary-50 text-primary-700 dark:bg-primary-900/50 dark:text-primary-300'
					: 'text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700'}"
				onclick={() => (open = false)}
			>
				<svg class="w-5 h-5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
					<path stroke-linecap="round" stroke-linejoin="round" d={link.icon} />
				</svg>
				{link.label}
			</a>
		{/each}
	</nav>
</aside>

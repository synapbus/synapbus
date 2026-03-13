<script lang="ts">
	import { goto } from '$app/navigation';
	import { user, logout } from '$lib/stores/auth';
	import { theme, toggleTheme } from '$lib/stores/theme';

	let { onMenuClick }: { onMenuClick: () => void } = $props();

	let searchQuery = $state('');
	let showUserMenu = $state(false);

	function handleSearch(e: SubmitEvent) {
		e.preventDefault();
		if (searchQuery.trim()) {
			goto(`/conversations?q=${encodeURIComponent(searchQuery.trim())}`);
			searchQuery = '';
		}
	}

	async function handleLogout() {
		await logout();
		goto('/login');
	}
</script>

<header class="sticky top-0 z-20 flex items-center gap-4 h-16 px-4 lg:px-6 bg-white dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700">
	<!-- Mobile menu button -->
	<button class="lg:hidden p-2 text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200" onclick={onMenuClick}>
		<svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
			<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16" />
		</svg>
	</button>

	<!-- Search -->
	<form class="flex-1 max-w-lg" onsubmit={handleSearch}>
		<div class="relative">
			<svg class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
				<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
			</svg>
			<input
				type="search"
				placeholder="Search messages..."
				class="w-full pl-10 pr-4 py-2 text-sm bg-gray-100 dark:bg-gray-700 border-none rounded-lg focus:ring-2 focus:ring-primary-500 outline-none"
				bind:value={searchQuery}
			/>
		</div>
	</form>

	<div class="flex items-center gap-2">
		<!-- Theme toggle -->
		<button
			class="p-2 text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 rounded-lg"
			onclick={toggleTheme}
			title="Toggle dark mode"
		>
			{#if $theme === 'dark'}
				<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />
				</svg>
			{:else}
				<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
					<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z" />
				</svg>
			{/if}
		</button>

		<!-- User menu -->
		<div class="relative">
			<button
				class="flex items-center gap-2 p-2 text-sm text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700 rounded-lg"
				onclick={() => (showUserMenu = !showUserMenu)}
			>
				<div class="w-8 h-8 rounded-full bg-primary-100 dark:bg-primary-900 flex items-center justify-center text-primary-700 dark:text-primary-300 font-medium">
					{$user?.username?.charAt(0).toUpperCase() ?? '?'}
				</div>
				<span class="hidden sm:inline">{$user?.display_name ?? $user?.username ?? ''}</span>
			</button>

			{#if showUserMenu}
				<div class="absolute right-0 mt-2 w-48 card py-1 shadow-lg">
					<a href="/settings" class="block px-4 py-2 text-sm text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-700" onclick={() => (showUserMenu = false)}>
						Settings
					</a>
					<button
						class="block w-full text-left px-4 py-2 text-sm text-red-600 dark:text-red-400 hover:bg-gray-100 dark:hover:bg-gray-700"
						onclick={handleLogout}
					>
						Logout
					</button>
				</div>
			{/if}
		</div>
	</div>
</header>

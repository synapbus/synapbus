<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { checkAuth, user, loading } from '$lib/stores/auth';
	import { SSEClient } from '$lib/api/sse';
	import Sidebar from '$lib/components/Sidebar.svelte';
	import Header from '$lib/components/Header.svelte';

	let { children } = $props();
	let sidebarOpen = $state(false);
	let sseClient: SSEClient | null = null;

	onMount(async () => {
		const isLoginPage = $page.url.pathname === '/login';

		const authenticated = await checkAuth();
		if (!authenticated && !isLoginPage) {
			goto(`/login?return=${encodeURIComponent($page.url.pathname)}`);
			return;
		}

		if (authenticated) {
			sseClient = new SSEClient();
			sseClient.connect();
		}

		return () => {
			sseClient?.disconnect();
		};
	});

	$effect(() => {
		if ($user && sseClient && !sseClient.connected) {
			sseClient.connect();
		}
	});

	$: isLoginPage = $page.url.pathname === '/login';
</script>

{#if $loading}
	<div class="flex items-center justify-center min-h-screen">
		<div class="w-8 h-8 border-4 border-primary-200 border-t-primary-600 rounded-full animate-spin"></div>
	</div>
{:else if isLoginPage}
	{@render children()}
{:else if $user}
	<div class="min-h-screen bg-gray-50 dark:bg-gray-900">
		<Sidebar bind:open={sidebarOpen} />
		<div class="lg:ml-64">
			<Header onMenuClick={() => (sidebarOpen = !sidebarOpen)} />
			<main class="p-4 lg:p-6">
				{@render children()}
			</main>
		</div>
	</div>
{/if}

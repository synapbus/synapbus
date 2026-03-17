<script lang="ts">
	import '../app.css';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { checkAuth, user, loading } from '$lib/stores/auth';
	import { SSEClient } from '$lib/api/sse';
	import { notifications } from '$lib/stores/notifications';
	import { fontSize, applyFontSize } from '$lib/stores/fontSize';
	import { version as versionApi } from '$lib/api/client';
	import Sidebar from '$lib/components/Sidebar.svelte';
	import Header from '$lib/components/Header.svelte';
	import ThreadPanel from '$lib/components/ThreadPanel.svelte';

	let { children } = $props();
	let sseClient: SSEClient | null = $state(null);
	let sseUnsubscribe: (() => void) | null = $state(null);
	let initialized = $state(false);
	let sidebarOpen = $state(false);
	let versionStr = $state('');
	let repoUrl = $state('https://github.com/synapbus/synapbus');

	let isLoginPage = $derived($page.url.pathname === '/login');

	function setupNotifications(client: SSEClient) {
		notifications.initialize();
		return client.onEvent((event) => {
			if (event.type === 'new_message') {
				const d = event.data;
				if (d.channel) {
					notifications.incrementUnread('channel', d.channel);
				} else if (d.from_agent) {
					notifications.incrementUnread('dm', d.from_agent);
				}
			} else if (event.type === 'unread_update') {
				const d = event.data;
				const type = d.type === 'channel' ? 'channel' : 'dm';
				notifications.setUnread(type as 'channel' | 'dm', d.target, d.count ?? 0);
			}
		});
	}

	// Apply font size on mount
	$effect(() => {
		applyFontSize($fontSize);
	});

	$effect(() => {
		if (!initialized) {
			initialized = true;
			// Load version
			versionApi.get().then((v) => {
				versionStr = v.version;
				repoUrl = v.repo || repoUrl;
			}).catch(() => { versionStr = 'dev'; });

			// Register service worker
			if ('serviceWorker' in navigator) {
				navigator.serviceWorker.register('/sw.js').catch(() => {});
			}

			checkAuth().then((authenticated) => {
				if (!authenticated && !isLoginPage) {
					goto(`/login?return=${encodeURIComponent($page.url.pathname)}`);
				} else if (authenticated) {
					sseClient = new SSEClient();
					sseClient.connect();
					sseUnsubscribe = setupNotifications(sseClient);
				}
			});
		}
	});

	$effect(() => {
		if ($user && !sseClient) {
			sseClient = new SSEClient();
			sseClient.connect();
			sseUnsubscribe = setupNotifications(sseClient);
		} else if ($user && sseClient && !sseClient.connected) {
			sseClient.connect();
		}
	});

	$effect(() => {
		return () => {
			sseUnsubscribe?.();
			sseClient?.disconnect();
			notifications.reset();
		};
	});
</script>

{#if $loading}
	<div class="flex items-center justify-center min-h-screen bg-bg-primary">
		<div class="flex flex-col items-center gap-3">
			<div class="w-8 h-8 border-2 border-border-active border-t-accent-blue rounded-full animate-spin"></div>
			<span class="text-xs text-text-secondary">Loading...</span>
		</div>
	</div>
{:else if isLoginPage}
	{@render children()}
{:else if $user}
	<div class="h-screen flex overflow-hidden bg-bg-primary">
		<Sidebar open={sidebarOpen} onclose={() => (sidebarOpen = false)} />
		<div class="md:ml-[260px] flex-1 flex flex-col min-w-0">
			<Header onMenuToggle={() => (sidebarOpen = !sidebarOpen)} />
			<main class="flex-1 overflow-y-auto flex flex-col">
				{@render children()}
			</main>
			<!-- Version Footer -->
			{#if versionStr}
				<footer class="px-5 py-2 border-t border-border flex items-center justify-between text-[10px] text-text-secondary flex-shrink-0">
					<span>SynapBus</span>
					<a href="{repoUrl}" target="_blank" rel="noopener" class="hover:text-text-primary transition-colors">
						{versionStr}
					</a>
				</footer>
			{/if}
		</div>
		<ThreadPanel />
	</div>
{/if}

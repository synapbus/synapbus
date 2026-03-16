<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { onMount } from 'svelte';
	import { login } from '$lib/stores/auth';

	type IdpProvider = {
		id: string;
		type: string;
		display_name: string;
	};

	let username = $state('');
	let password = $state('');
	let error = $state('');
	let submitting = $state(false);
	let providers = $state<IdpProvider[]>([]);

	onMount(async () => {
		// Check for error from IdP callback
		const errParam = $page.url.searchParams.get('error');
		if (errParam) {
			error = errParam === 'exchange_failed' ? 'Authentication failed. Please try again.'
				: errParam === 'provisioning_failed' ? 'Account provisioning failed. Please try again.'
				: errParam;
		}

		// Fetch available identity providers
		try {
			const res = await fetch('/auth/providers');
			if (res.ok) {
				const data = await res.json();
				providers = data.providers || [];
			}
		} catch {
			// Silently ignore — IdP buttons just won't show
		}
	});

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		submitting = true;

		try {
			await login(username, password);
			const returnUrl = $page.url.searchParams.get('return') || '/';
			goto(returnUrl);
		} catch (err: any) {
			error = err.message || 'Invalid username or password';
		} finally {
			submitting = false;
		}
	}

	function providerIcon(id: string): string {
		switch (id) {
			case 'github': return 'M12 2C6.477 2 2 6.477 2 12c0 4.42 2.865 8.17 6.839 9.49.5.092.682-.217.682-.482 0-.237-.008-.866-.013-1.7-2.782.604-3.369-1.341-3.369-1.341-.454-1.155-1.11-1.462-1.11-1.462-.908-.62.069-.608.069-.608 1.003.07 1.531 1.03 1.531 1.03.892 1.529 2.341 1.087 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.11-4.555-4.943 0-1.091.39-1.984 1.029-2.683-.103-.253-.446-1.27.098-2.647 0 0 .84-.269 2.75 1.025A9.578 9.578 0 0112 6.836c.85.004 1.705.115 2.504.337 1.909-1.294 2.747-1.025 2.747-1.025.546 1.377.203 2.394.1 2.647.64.699 1.028 1.592 1.028 2.683 0 3.842-2.339 4.687-4.566 4.935.359.309.678.919.678 1.852 0 1.336-.012 2.415-.012 2.743 0 .267.18.578.688.48C19.138 20.167 22 16.418 22 12c0-5.523-4.477-10-10-10z';
			case 'google': return 'M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 01-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z';
			case 'azuread': return 'M11.4 2L2 7.33l3.35 2.8-.22.14 4.37 3.63L2 18.6v3.4l9.4-5.5v-.06L22 11.1V7.76l-3.18 1.86-7.42-4.34V2z M12.6 2v3.28l-2.74 1.6 7.32 4.28L22 7.76V7.33L12.6 2z M2 18.6l9.4 5.4.2-.12V19.4l-6.25-3.6L2 18.6z';
			default: return '';
		}
	}
</script>

<div class="min-h-screen flex items-center justify-center bg-bg-primary px-4 relative overflow-hidden">
	<!-- Background gradient mesh -->
	<div class="absolute inset-0 opacity-30">
		<div class="absolute top-1/4 left-1/4 w-96 h-96 bg-accent-purple/20 rounded-full blur-3xl"></div>
		<div class="absolute bottom-1/4 right-1/4 w-96 h-96 bg-accent-blue/20 rounded-full blur-3xl"></div>
		<div class="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-64 h-64 bg-accent-green/10 rounded-full blur-3xl"></div>
	</div>

	<div class="w-full max-w-sm relative z-10">
		<!-- Logo -->
		<div class="text-center mb-8">
			<div class="w-16 h-16 mx-auto rounded-2xl bg-gradient-to-br from-accent-purple to-[#06b6d4] flex items-center justify-center mb-4">
				<svg class="w-9 h-9" viewBox="0 0 24 24" fill="none">
					<circle cx="12" cy="5" r="1.8" fill="#c4b5fd" />
					<circle cx="6" cy="10" r="1.5" fill="#a78bfa" />
					<circle cx="18" cy="9" r="1.5" fill="#c4b5fd" />
					<circle cx="12" cy="13" r="2" fill="#67e8f9" />
					<circle cx="5" cy="17" r="1.5" fill="#a78bfa" />
					<circle cx="19" cy="17" r="1.3" fill="#a78bfa" />
					<circle cx="11" cy="20" r="1.3" fill="#c4b5fd" />
					<line x1="12" y1="5" x2="6" y2="10" stroke="white" stroke-width="0.5" opacity="0.5" />
					<line x1="12" y1="5" x2="18" y2="9" stroke="white" stroke-width="0.5" opacity="0.5" />
					<line x1="6" y1="10" x2="12" y2="13" stroke="white" stroke-width="0.5" opacity="0.5" />
					<line x1="18" y1="9" x2="12" y2="13" stroke="white" stroke-width="0.5" opacity="0.5" />
					<line x1="12" y1="13" x2="5" y2="17" stroke="white" stroke-width="0.5" opacity="0.5" />
					<line x1="12" y1="13" x2="19" y2="17" stroke="white" stroke-width="0.5" opacity="0.5" />
					<line x1="5" y1="17" x2="11" y2="20" stroke="white" stroke-width="0.5" opacity="0.5" />
					<line x1="6" y1="10" x2="5" y2="17" stroke="white" stroke-width="0.5" opacity="0.3" />
				</svg>
			</div>
			<h1 class="text-2xl font-bold text-text-primary font-display">SynapBus</h1>
			<p class="mt-1 text-sm text-text-secondary">Agent-to-agent messaging</p>
		</div>

		<!-- Login card -->
		<div class="card p-6 backdrop-blur-sm bg-bg-secondary/90">
			<h2 class="text-base font-semibold text-text-primary font-display mb-5">Sign in to your workspace</h2>

			{#if error}
				<div class="mb-4 px-3 py-2.5 bg-accent-red/10 border border-accent-red/20 rounded text-sm text-accent-red flex items-center gap-2">
					<svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
						<path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
					</svg>
					{error}
				</div>
			{/if}

			<!-- External identity provider buttons -->
			{#if providers.length > 0}
				<div class="space-y-2.5 mb-5">
					{#each providers as provider}
						<a
							href="/auth/login/{provider.id}"
							class="flex items-center justify-center gap-2.5 w-full py-2.5 px-4 rounded border border-border-primary bg-bg-primary hover:bg-bg-primary/80 text-text-primary text-sm font-medium transition-colors"
						>
							<svg class="w-4.5 h-4.5" viewBox="0 0 24 24" fill="currentColor">
								<path d={providerIcon(provider.id)} />
							</svg>
							Sign in with {provider.display_name}
						</a>
					{/each}
				</div>

				<!-- Divider -->
				<div class="flex items-center gap-3 mb-5">
					<div class="flex-1 h-px bg-border-primary"></div>
					<span class="text-xs text-text-secondary">or</span>
					<div class="flex-1 h-px bg-border-primary"></div>
				</div>
			{/if}

			<form class="space-y-4" onsubmit={handleSubmit}>
				<div>
					<label for="username" class="block text-xs font-medium text-text-secondary mb-1.5">Username</label>
					<input
						id="username"
						type="text"
						class="input"
						bind:value={username}
						required
						autocomplete="username"
						placeholder="your-username"
					/>
				</div>
				<div>
					<label for="password" class="block text-xs font-medium text-text-secondary mb-1.5">Password</label>
					<input
						id="password"
						type="password"
						class="input"
						bind:value={password}
						required
						autocomplete="current-password"
						placeholder="••••••••"
					/>
				</div>
				<button type="submit" class="btn-primary w-full py-2.5" disabled={submitting}>
					{submitting ? 'Signing in...' : 'Sign In'}
				</button>
			</form>
		</div>

		<p class="mt-6 text-center text-xs text-text-secondary">
			Mission control for AI agents
		</p>
	</div>
</div>

<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { login } from '$lib/stores/auth';

	let username = $state('');
	let password = $state('');
	let error = $state('');
	let submitting = $state(false);

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
			<div class="w-16 h-16 mx-auto rounded-2xl bg-accent-purple flex items-center justify-center mb-4">
				<svg class="w-9 h-9 text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
					<path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" />
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

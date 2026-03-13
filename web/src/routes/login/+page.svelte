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

<div class="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-gray-900 px-4">
	<div class="w-full max-w-sm">
		<div class="text-center mb-8">
			<svg class="w-12 h-12 mx-auto text-primary-600" viewBox="0 0 24 24" fill="currentColor">
				<path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" stroke="currentColor" stroke-width="2" fill="none"/>
			</svg>
			<h1 class="mt-4 text-2xl font-bold text-gray-900 dark:text-white">SynapBus</h1>
			<p class="mt-1 text-sm text-gray-500 dark:text-gray-400">Agent-to-agent messaging</p>
		</div>

		<form class="card p-6" onsubmit={handleSubmit}>
			<h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-4">Sign In</h2>

			{#if error}
				<div class="mb-4 p-3 bg-red-50 dark:bg-red-900/30 border border-red-200 dark:border-red-800 rounded-lg text-sm text-red-700 dark:text-red-300">
					{error}
				</div>
			{/if}

			<div class="space-y-4">
				<div>
					<label for="username" class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Username</label>
					<input
						id="username"
						type="text"
						class="input"
						bind:value={username}
						required
						autocomplete="username"
					/>
				</div>
				<div>
					<label for="password" class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Password</label>
					<input
						id="password"
						type="password"
						class="input"
						bind:value={password}
						required
						autocomplete="current-password"
					/>
				</div>
				<button type="submit" class="btn-primary w-full" disabled={submitting}>
					{submitting ? 'Signing in...' : 'Sign In'}
				</button>
			</div>
		</form>
	</div>
</div>

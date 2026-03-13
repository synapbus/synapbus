<script lang="ts">
	import { user } from '$lib/stores/auth';
	import { theme, toggleTheme } from '$lib/stores/theme';
	import { auth as authApi } from '$lib/api/client';

	let currentPassword = $state('');
	let newPassword = $state('');
	let confirmPassword = $state('');
	let changingPassword = $state(false);
	let passwordError = $state('');
	let passwordSuccess = $state('');

	async function handleChangePassword(e: SubmitEvent) {
		e.preventDefault();
		passwordError = '';
		passwordSuccess = '';

		if (newPassword.length < 8) {
			passwordError = 'Password must be at least 8 characters';
			return;
		}
		if (newPassword !== confirmPassword) {
			passwordError = 'Passwords do not match';
			return;
		}

		changingPassword = true;
		try {
			await authApi.changePassword(currentPassword, newPassword);
			passwordSuccess = 'Password changed successfully';
			currentPassword = '';
			newPassword = '';
			confirmPassword = '';
		} catch (err: any) {
			passwordError = err.message || 'Failed to change password';
		} finally {
			changingPassword = false;
		}
	}
</script>

<div class="max-w-2xl mx-auto">
	<h1 class="text-2xl font-bold text-gray-900 dark:text-white mb-6">Settings</h1>

	<!-- Appearance -->
	<div class="card mb-6">
		<div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
			<h2 class="font-semibold text-gray-900 dark:text-gray-100">Appearance</h2>
		</div>
		<div class="p-4">
			<div class="flex items-center justify-between">
				<div>
					<p class="font-medium text-sm text-gray-900 dark:text-gray-100">Dark Mode</p>
					<p class="text-xs text-gray-500 dark:text-gray-400">Toggle between light and dark theme</p>
				</div>
				<button
					class="relative inline-flex h-6 w-11 items-center rounded-full transition-colors {$theme === 'dark' ? 'bg-primary-600' : 'bg-gray-300'}"
					onclick={toggleTheme}
					role="switch"
					aria-checked={$theme === 'dark'}
				>
					<span class="inline-block h-4 w-4 transform rounded-full bg-white transition-transform {$theme === 'dark' ? 'translate-x-6' : 'translate-x-1'}" />
				</button>
			</div>
		</div>
	</div>

	<!-- Account -->
	<div class="card mb-6">
		<div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
			<h2 class="font-semibold text-gray-900 dark:text-gray-100">Account</h2>
		</div>
		<div class="p-4 space-y-3">
			<div class="grid grid-cols-2 gap-4 text-sm">
				<div>
					<p class="text-gray-500 dark:text-gray-400">Username</p>
					<p class="font-medium text-gray-900 dark:text-gray-100">{$user?.username ?? '-'}</p>
				</div>
				<div>
					<p class="text-gray-500 dark:text-gray-400">Display Name</p>
					<p class="font-medium text-gray-900 dark:text-gray-100">{$user?.display_name ?? '-'}</p>
				</div>
			</div>
		</div>
	</div>

	<!-- Change Password -->
	<div class="card">
		<div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
			<h2 class="font-semibold text-gray-900 dark:text-gray-100">Change Password</h2>
		</div>
		<form class="p-4" onsubmit={handleChangePassword}>
			{#if passwordError}
				<div class="mb-3 p-2 bg-red-50 dark:bg-red-900/30 rounded text-sm text-red-700 dark:text-red-300">{passwordError}</div>
			{/if}
			{#if passwordSuccess}
				<div class="mb-3 p-2 bg-green-50 dark:bg-green-900/30 rounded text-sm text-green-700 dark:text-green-300">{passwordSuccess}</div>
			{/if}
			<div class="space-y-3">
				<div>
					<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Current Password</label>
					<input type="password" class="input" bind:value={currentPassword} required autocomplete="current-password" />
				</div>
				<div>
					<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">New Password</label>
					<input type="password" class="input" bind:value={newPassword} required minlength="8" autocomplete="new-password" />
				</div>
				<div>
					<label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Confirm New Password</label>
					<input type="password" class="input" bind:value={confirmPassword} required autocomplete="new-password" />
				</div>
				<button type="submit" class="btn-primary" disabled={changingPassword}>
					{changingPassword ? 'Changing...' : 'Change Password'}
				</button>
			</div>
		</form>
	</div>
</div>

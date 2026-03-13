<script lang="ts">
	import { user } from '$lib/stores/auth';
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

<div class="p-5 max-w-2xl">
	<h1 class="text-xl font-bold text-text-primary font-display mb-5">Settings</h1>

	<!-- Account -->
	<div class="card mb-5">
		<div class="px-5 py-3 border-b border-border">
			<h2 class="font-semibold text-sm text-text-primary font-display">Account</h2>
		</div>
		<div class="p-5">
			<div class="grid grid-cols-2 gap-4 text-sm">
				<div>
					<p class="text-xs text-text-secondary mb-0.5">Username</p>
					<p class="font-medium text-text-primary">{$user?.username ?? '-'}</p>
				</div>
				<div>
					<p class="text-xs text-text-secondary mb-0.5">Display Name</p>
					<p class="font-medium text-text-primary">{$user?.display_name ?? '-'}</p>
				</div>
				<div>
					<p class="text-xs text-text-secondary mb-0.5">Role</p>
					<p class="font-medium text-text-primary">{$user?.role ?? '-'}</p>
				</div>
			</div>
		</div>
	</div>

	<!-- Quick links -->
	<div class="card mb-5">
		<div class="px-5 py-3 border-b border-border">
			<h2 class="font-semibold text-sm text-text-primary font-display">Management</h2>
		</div>
		<div class="divide-y divide-border">
			<a href="/settings/api-keys" class="flex items-center justify-between px-5 py-3 hover:bg-bg-tertiary/50 transition-colors">
				<div class="flex items-center gap-3">
					<svg class="w-4 h-4 text-text-secondary" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
						<path stroke-linecap="round" stroke-linejoin="round" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
					</svg>
					<div>
						<p class="text-sm text-text-primary font-medium">API Keys</p>
						<p class="text-xs text-text-secondary">Manage API keys for agent access</p>
					</div>
				</div>
				<svg class="w-4 h-4 text-text-secondary" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
					<path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7" />
				</svg>
			</a>
		</div>
	</div>

	<!-- Change Password -->
	<div class="card">
		<div class="px-5 py-3 border-b border-border">
			<h2 class="font-semibold text-sm text-text-primary font-display">Change Password</h2>
		</div>
		<form class="p-5" onsubmit={handleChangePassword}>
			{#if passwordError}
				<div class="mb-3 px-3 py-2 bg-accent-red/10 rounded text-xs text-accent-red">{passwordError}</div>
			{/if}
			{#if passwordSuccess}
				<div class="mb-3 px-3 py-2 bg-accent-green/10 rounded text-xs text-accent-green">{passwordSuccess}</div>
			{/if}
			<div class="space-y-3">
				<div>
					<label for="current-pw" class="block text-xs font-medium text-text-secondary mb-1.5">Current Password</label>
					<input id="current-pw" type="password" class="input" bind:value={currentPassword} required autocomplete="current-password" />
				</div>
				<div>
					<label for="new-pw" class="block text-xs font-medium text-text-secondary mb-1.5">New Password</label>
					<input id="new-pw" type="password" class="input" bind:value={newPassword} required minlength="8" autocomplete="new-password" />
				</div>
				<div>
					<label for="confirm-pw" class="block text-xs font-medium text-text-secondary mb-1.5">Confirm New Password</label>
					<input id="confirm-pw" type="password" class="input" bind:value={confirmPassword} required autocomplete="new-password" />
				</div>
				<button type="submit" class="btn-primary" disabled={changingPassword}>
					{changingPassword ? 'Changing...' : 'Change Password'}
				</button>
			</div>
		</form>
	</div>
</div>

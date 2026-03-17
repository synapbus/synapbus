<script lang="ts">
	import { user } from '$lib/stores/auth';
	import { auth as authApi, profile } from '$lib/api/client';
	import { fontSize, increaseFontSize, decreaseFontSize, MIN_SIZE, MAX_SIZE } from '$lib/stores/fontSize';

	let currentPassword = $state('');
	let newPassword = $state('');
	let confirmPassword = $state('');
	let changingPassword = $state(false);
	let passwordError = $state('');
	let passwordSuccess = $state('');

	// Display name editing
	let displayName = $state('');
	let savingName = $state(false);
	let nameSuccess = $state('');
	let nameError = $state('');

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized && $user) {
			_initialized = true;
			displayName = $user.display_name || '';
		}
	});

	async function handleSaveDisplayName() {
		if (!displayName.trim()) {
			nameError = 'Display name cannot be empty';
			return;
		}
		savingName = true;
		nameError = '';
		nameSuccess = '';
		try {
			const res = await profile.update({ display_name: displayName.trim() });
			user.set(res.user);
			nameSuccess = 'Display name updated';
			setTimeout(() => (nameSuccess = ''), 3000);
		} catch (err: any) {
			nameError = err.message || 'Failed to update display name';
		} finally {
			savingName = false;
		}
	}

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

	// Push notification state
	let pushSupported = $state(false);
	let pushEnabled = $state(false);
	let pushError = $state('');

	$effect(() => {
		if (typeof window !== 'undefined' && 'serviceWorker' in navigator && 'PushManager' in window) {
			pushSupported = true;
			navigator.serviceWorker.ready.then((reg) => {
				reg.pushManager.getSubscription().then((sub) => {
					pushEnabled = !!sub;
				});
			});
		}
	});

	async function togglePush() {
		pushError = '';
		try {
			if (pushEnabled) {
				const reg = await navigator.serviceWorker.ready;
				const sub = await reg.pushManager.getSubscription();
				if (sub) {
					try {
						const { push: pushApi } = await import('$lib/api/client');
						await pushApi.unsubscribe(sub.endpoint);
					} catch { /* backend cleanup best-effort */ }
					await sub.unsubscribe();
					pushEnabled = false;
				}
			} else {
				const { push: pushApi } = await import('$lib/api/client');
				const keyRes = await pushApi.vapidKey();
				const reg = await navigator.serviceWorker.ready;
				const sub = await reg.pushManager.subscribe({
					userVisibleOnly: true,
					applicationServerKey: urlBase64ToUint8Array(keyRes.public_key)
				});
				const subJson = sub.toJSON();
				await pushApi.subscribe({
					endpoint: sub.endpoint,
					keys: {
						p256dh: subJson.keys?.p256dh || '',
						auth: subJson.keys?.auth || ''
					}
				});
				pushEnabled = true;
			}
		} catch (err: any) {
			pushError = err.message || 'Failed to toggle push notifications';
		}
	}

	function urlBase64ToUint8Array(base64String: string): Uint8Array {
		const padding = '='.repeat((4 - base64String.length % 4) % 4);
		const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
		const rawData = window.atob(base64);
		const outputArray = new Uint8Array(rawData.length);
		for (let i = 0; i < rawData.length; ++i) {
			outputArray[i] = rawData.charCodeAt(i);
		}
		return outputArray;
	}
</script>

<div class="p-5 max-w-2xl">
	<h1 class="text-xl font-bold text-text-primary font-display mb-5">Settings</h1>

	<!-- Account -->
	<div class="card mb-5">
		<div class="px-5 py-3 border-b border-border">
			<h2 class="font-semibold text-sm text-text-primary font-display">Account</h2>
		</div>
		<div class="p-5 space-y-4">
			<div class="grid grid-cols-2 gap-4 text-sm">
				<div>
					<p class="text-xs text-text-secondary mb-0.5">Username</p>
					<p class="font-medium text-text-primary">{$user?.username ?? '-'}</p>
				</div>
				<div>
					<p class="text-xs text-text-secondary mb-0.5">Role</p>
					<p class="font-medium text-text-primary">{$user?.role ?? '-'}</p>
				</div>
			</div>

			<!-- Editable Display Name -->
			<div>
				<label for="display-name" class="block text-xs font-medium text-text-secondary mb-1.5">Display Name</label>
				<div class="flex items-center gap-2">
					<input
						id="display-name"
						type="text"
						class="input flex-1"
						bind:value={displayName}
						placeholder="Your display name"
					/>
					<button
						class="btn-primary text-xs"
						onclick={handleSaveDisplayName}
						disabled={savingName}
					>
						{savingName ? 'Saving...' : 'Save'}
					</button>
				</div>
				{#if nameError}
					<p class="text-xs text-accent-red mt-1">{nameError}</p>
				{/if}
				{#if nameSuccess}
					<p class="text-xs text-accent-green mt-1">{nameSuccess}</p>
				{/if}
			</div>
		</div>
	</div>

	<!-- Appearance -->
	<div class="card mb-5">
		<div class="px-5 py-3 border-b border-border">
			<h2 class="font-semibold text-sm text-text-primary font-display">Appearance</h2>
		</div>
		<div class="p-5 space-y-4">
			<!-- Font Size -->
			<div>
				<p class="text-xs font-medium text-text-secondary mb-2">Font Size</p>
				<div class="flex items-center gap-3">
					<button
						class="w-8 h-8 rounded-lg border border-border flex items-center justify-center text-text-primary hover:bg-bg-tertiary transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
						onclick={decreaseFontSize}
						disabled={$fontSize <= MIN_SIZE}
						title="Decrease font size"
					>
						<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
							<path stroke-linecap="round" stroke-linejoin="round" d="M20 12H4" />
						</svg>
					</button>
					<span class="text-sm font-mono text-text-primary w-12 text-center">{$fontSize}px</span>
					<button
						class="w-8 h-8 rounded-lg border border-border flex items-center justify-center text-text-primary hover:bg-bg-tertiary transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
						onclick={increaseFontSize}
						disabled={$fontSize >= MAX_SIZE}
						title="Increase font size"
					>
						<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
							<path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
						</svg>
					</button>
				</div>
				<p class="text-[10px] text-text-secondary mt-1">Range: {MIN_SIZE}px – {MAX_SIZE}px. Persists across sessions.</p>
			</div>
		</div>
	</div>

	<!-- Push Notifications -->
	{#if pushSupported}
		<div class="card mb-5">
			<div class="px-5 py-3 border-b border-border">
				<h2 class="font-semibold text-sm text-text-primary font-display">Push Notifications</h2>
			</div>
			<div class="p-5">
				<div class="flex items-center justify-between">
					<div>
						<p class="text-sm text-text-primary">Desktop notifications</p>
						<p class="text-[10px] text-text-secondary mt-0.5">Get notified for high-priority DMs and @mentions</p>
					</div>
					<button
						class="relative w-11 h-6 rounded-full transition-colors {pushEnabled ? 'bg-accent-green' : 'bg-bg-tertiary'}"
						onclick={togglePush}
					>
						<div
							class="absolute top-0.5 w-5 h-5 rounded-full bg-white shadow transition-transform {pushEnabled ? 'translate-x-5' : 'translate-x-0.5'}"
						></div>
					</button>
				</div>
				{#if pushError}
					<p class="text-xs text-accent-red mt-2">{pushError}</p>
				{/if}
			</div>
		</div>
	{/if}

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

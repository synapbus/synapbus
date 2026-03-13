<script lang="ts">
	import { apiKeys, agents as agentsApi } from '$lib/api/client';

	let keys = $state<any[]>([]);
	let agentList = $state<any[]>([]);
	let loadingData = $state(true);

	// Create form state
	let showCreate = $state(false);
	let creating = $state(false);
	let createError = $state('');
	let newKeyName = $state('');
	let newKeyAgentId = $state<number | undefined>(undefined);
	let newKeyPermRead = $state(true);
	let newKeyPermWrite = $state(true);
	let newKeyPermAdmin = $state(false);
	let newKeyChannels = $state('');
	let newKeyReadOnly = $state(false);
	let newKeyExpiry = $state('');

	// Created key display
	let createdKey = $state<{ key: any; api_key: string; mcp_config: any } | null>(null);
	let copied = $state(false);

	// Revoke
	let confirmRevokeId = $state<number | null>(null);
	let revoking = $state(false);

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			loadData();
		}
	});

	async function loadData() {
		loadingData = true;
		try {
			const [keysRes, agRes] = await Promise.all([
				apiKeys.list(),
				agentsApi.list()
			]);
			keys = keysRes.keys ?? [];
			agentList = agRes.agents ?? [];
		} catch {
			// handled
		} finally {
			loadingData = false;
		}
	}

	async function handleCreate(e: SubmitEvent) {
		e.preventDefault();
		if (!newKeyName.trim()) {
			createError = 'Key name is required';
			return;
		}
		creating = true;
		createError = '';
		try {
			const permissions: Record<string, boolean> = {};
			if (newKeyPermRead) permissions.read = true;
			if (newKeyPermWrite) permissions.write = true;
			if (newKeyPermAdmin) permissions.admin = true;

			const channels = newKeyChannels.trim()
				? newKeyChannels.split(',').map((s) => s.trim()).filter(Boolean)
				: undefined;

			const res = await apiKeys.create({
				name: newKeyName.trim(),
				agent_id: newKeyAgentId,
				permissions: Object.keys(permissions).length > 0 ? permissions : undefined,
				allowed_channels: channels,
				read_only: newKeyReadOnly || undefined,
				expires_at: newKeyExpiry || undefined
			});

			createdKey = res;
			newKeyName = '';
			newKeyAgentId = undefined;
			newKeyPermRead = true;
			newKeyPermWrite = true;
			newKeyPermAdmin = false;
			newKeyChannels = '';
			newKeyReadOnly = false;
			newKeyExpiry = '';
			showCreate = false;
			await loadData();
		} catch (err: any) {
			createError = err.message || 'Failed to create API key';
		} finally {
			creating = false;
		}
	}

	async function handleRevoke(id: number) {
		revoking = true;
		try {
			await apiKeys.revoke(id);
			confirmRevokeId = null;
			await loadData();
		} catch (err: any) {
			alert(err.message || 'Failed to revoke key');
		} finally {
			revoking = false;
		}
	}

	async function copyToClipboard(text: string) {
		try {
			await navigator.clipboard.writeText(text);
			copied = true;
			setTimeout(() => (copied = false), 2000);
		} catch {
			// fallback
			const ta = document.createElement('textarea');
			ta.value = text;
			document.body.appendChild(ta);
			ta.select();
			document.execCommand('copy');
			document.body.removeChild(ta);
			copied = true;
			setTimeout(() => (copied = false), 2000);
		}
	}

	function formatDate(iso: string): string {
		if (!iso) return '-';
		return new Date(iso).toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' });
	}

	function getMcpConfig(apiKey: string): string {
		return JSON.stringify({
			mcpServers: {
				synapbus: {
					url: `${typeof window !== 'undefined' ? window.location.origin : 'http://localhost:8080'}/mcp`,
					headers: {
						Authorization: `Bearer ${apiKey}`
					}
				}
			}
		}, null, 2);
	}

	function getClaudeConfig(apiKey: string): string {
		return JSON.stringify({
			synapbus: {
				type: 'streamableHttp',
				url: `${typeof window !== 'undefined' ? window.location.origin : 'http://localhost:8080'}/mcp`,
				headers: {
					Authorization: `Bearer ${apiKey}`
				}
			}
		}, null, 2);
	}
</script>

<div class="p-5 max-w-5xl">
	<div class="flex items-center justify-between mb-5">
		<div class="flex items-center gap-3">
			<a href="/settings" class="text-text-secondary hover:text-text-primary transition-colors" aria-label="Back to settings">
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
					<path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
				</svg>
			</a>
			<h1 class="text-xl font-bold text-text-primary font-display">API Keys</h1>
		</div>
		<button
			class="btn-primary flex items-center gap-1.5"
			onclick={() => { showCreate = !showCreate; createdKey = null; }}
		>
			{#if showCreate}
				Cancel
			{:else}
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
					<path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
				</svg>
				Create API Key
			{/if}
		</button>
	</div>

	<!-- Created key display -->
	{#if createdKey}
		<div class="card mb-5 border-accent-yellow/30">
			<div class="px-5 py-3 border-b border-border bg-accent-yellow/5">
				<div class="flex items-center gap-2">
					<svg class="w-4 h-4 text-accent-yellow" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
						<path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4.5c-.77-.833-2.694-.833-3.464 0L3.34 16.5c-.77.833.192 2.5 1.732 2.5z" />
					</svg>
					<span class="text-sm font-semibold text-accent-yellow">This key will only be shown once. Save it now.</span>
				</div>
			</div>
			<div class="p-5 space-y-4">
				<!-- The key -->
				<div>
					<p class="block text-xs font-medium text-text-secondary mb-1.5">API Key</p>
					<div class="flex items-center gap-2">
						<code class="flex-1 p-3 bg-bg-primary rounded-lg text-sm font-mono text-accent-green break-all select-all border border-accent-green/20">{createdKey.api_key}</code>
						<button
							class="flex-shrink-0 p-2.5 rounded-lg bg-bg-tertiary hover:bg-border-active text-text-secondary hover:text-text-primary transition-all"
							onclick={() => copyToClipboard(createdKey?.api_key ?? '')}
							title="Copy to clipboard"
						>
							{#if copied}
								<svg class="w-4 h-4 text-accent-green" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
									<path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
								</svg>
							{:else}
								<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
									<path stroke-linecap="round" stroke-linejoin="round" d="M8 5H6a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2v-1M8 5a2 2 0 002 2h2a2 2 0 002-2M8 5a2 2 0 012-2h2a2 2 0 012 2m0 0h2a2 2 0 012 2v3m2 4H10m0 0l3-3m-3 3l3 3" />
								</svg>
							{/if}
						</button>
					</div>
				</div>

				<!-- MCP Config -->
				<div>
					<p class="block text-xs font-medium text-text-secondary mb-1.5">MCP Config (for MCP clients)</p>
					<pre class="p-3 bg-bg-primary rounded-lg text-xs font-mono text-text-primary/80 overflow-x-auto border border-border">{getMcpConfig(createdKey.api_key)}</pre>
				</div>

				<!-- Claude Code config -->
				<div>
					<p class="block text-xs font-medium text-text-secondary mb-1.5">Claude Code Config (add to ~/.claude/mcp.json)</p>
					<pre class="p-3 bg-bg-primary rounded-lg text-xs font-mono text-text-primary/80 overflow-x-auto border border-border">{getClaudeConfig(createdKey.api_key)}</pre>
				</div>
			</div>
			<div class="px-5 py-3 border-t border-border">
				<button
					class="btn-secondary text-xs"
					onclick={() => (createdKey = null)}
				>
					Dismiss
				</button>
			</div>
		</div>
	{/if}

	<!-- Create form -->
	{#if showCreate}
		<form class="card p-5 mb-5" onsubmit={handleCreate}>
			<h3 class="font-semibold text-sm text-text-primary font-display mb-4">Create New API Key</h3>
			{#if createError}
				<div class="mb-3 px-3 py-2 bg-accent-red/10 rounded text-xs text-accent-red">{createError}</div>
			{/if}
			<div class="space-y-4">
				<div>
					<label for="key-name" class="block text-xs font-medium text-text-secondary mb-1.5">Name</label>
					<input id="key-name" type="text" class="input" placeholder="e.g. production-researcher" bind:value={newKeyName} required />
				</div>

				<div>
					<label for="key-agent" class="block text-xs font-medium text-text-secondary mb-1.5">Agent (optional)</label>
					<select id="key-agent" class="input" bind:value={newKeyAgentId}>
						<option value={undefined}>No specific agent</option>
						{#each agentList as agent}
							<option value={agent.id}>{agent.display_name || agent.name} (@{agent.name})</option>
						{/each}
					</select>
				</div>

				<div>
					<p class="block text-xs font-medium text-text-secondary mb-2">Permissions</p>
					<div class="flex flex-wrap gap-4">
						<label class="flex items-center gap-2 text-sm text-text-primary cursor-pointer">
							<input type="checkbox" bind:checked={newKeyPermRead} class="rounded bg-bg-input border-border text-accent-green focus:ring-accent-green" />
							Read
						</label>
						<label class="flex items-center gap-2 text-sm text-text-primary cursor-pointer">
							<input type="checkbox" bind:checked={newKeyPermWrite} class="rounded bg-bg-input border-border text-accent-green focus:ring-accent-green" />
							Write
						</label>
						<label class="flex items-center gap-2 text-sm text-text-primary cursor-pointer">
							<input type="checkbox" bind:checked={newKeyPermAdmin} class="rounded bg-bg-input border-border text-accent-green focus:ring-accent-green" />
							Admin
						</label>
					</div>
				</div>

				<div>
					<label for="key-channels" class="block text-xs font-medium text-text-secondary mb-1.5">Allowed Channels (comma-separated, leave empty for all)</label>
					<input id="key-channels" type="text" class="input" placeholder="general, research, alerts" bind:value={newKeyChannels} />
				</div>

				<label class="flex items-center gap-2 text-sm text-text-primary cursor-pointer">
					<input type="checkbox" bind:checked={newKeyReadOnly} class="rounded bg-bg-input border-border text-accent-green focus:ring-accent-green" />
					Read-only mode
				</label>

				<div>
					<label for="key-expiry" class="block text-xs font-medium text-text-secondary mb-1.5">Expiry Date (optional)</label>
					<input id="key-expiry" type="date" class="input" bind:value={newKeyExpiry} />
				</div>

				<button type="submit" class="btn-primary" disabled={creating}>
					{creating ? 'Creating...' : 'Create API Key'}
				</button>
			</div>
		</form>
	{/if}

	<!-- Keys list -->
	<div class="card">
		<div class="px-5 py-3 border-b border-border">
			<h2 class="font-semibold text-sm text-text-primary font-display">Your API Keys</h2>
		</div>
		{#if loadingData}
			<div class="p-5 space-y-3">
				{#each Array(3) as _}
					<div class="skeleton h-10 w-full"></div>
				{/each}
			</div>
		{:else if keys.length === 0}
			<div class="p-8 text-center">
				<svg class="w-10 h-10 mx-auto mb-3 text-border-active" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1">
					<path stroke-linecap="round" stroke-linejoin="round" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
				</svg>
				<p class="text-sm text-text-secondary">No API keys yet. Create one to get started.</p>
			</div>
		{:else}
			<div class="overflow-x-auto">
				<table class="w-full text-sm">
					<thead>
						<tr class="border-b border-border">
							<th class="text-left px-5 py-2.5 text-xs font-medium text-text-secondary">Name</th>
							<th class="text-left px-5 py-2.5 text-xs font-medium text-text-secondary">Prefix</th>
							<th class="text-left px-5 py-2.5 text-xs font-medium text-text-secondary">Permissions</th>
							<th class="text-left px-5 py-2.5 text-xs font-medium text-text-secondary">Channels</th>
							<th class="text-left px-5 py-2.5 text-xs font-medium text-text-secondary">Expires</th>
							<th class="text-left px-5 py-2.5 text-xs font-medium text-text-secondary">Created</th>
							<th class="text-right px-5 py-2.5 text-xs font-medium text-text-secondary">Actions</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-border">
						{#each keys as key}
							<tr class="hover:bg-bg-tertiary/30 transition-colors">
								<td class="px-5 py-2.5 text-text-primary font-medium">{key.name}</td>
								<td class="px-5 py-2.5 font-mono text-xs text-text-secondary">{key.prefix ?? 'sb_...'}</td>
								<td class="px-5 py-2.5">
									<div class="flex gap-1">
										{#if key.permissions?.read}
											<span class="badge bg-accent-blue/20 text-accent-blue">read</span>
										{/if}
										{#if key.permissions?.write}
											<span class="badge bg-accent-green/20 text-accent-green">write</span>
										{/if}
										{#if key.permissions?.admin}
											<span class="badge bg-accent-purple/20 text-accent-purple">admin</span>
										{/if}
										{#if key.read_only}
											<span class="badge bg-accent-yellow/20 text-accent-yellow">RO</span>
										{/if}
									</div>
								</td>
								<td class="px-5 py-2.5 text-xs text-text-secondary">
									{key.allowed_channels?.length ? key.allowed_channels.join(', ') : 'All'}
								</td>
								<td class="px-5 py-2.5 text-xs text-text-secondary">{formatDate(key.expires_at)}</td>
								<td class="px-5 py-2.5 text-xs text-text-secondary">{formatDate(key.created_at)}</td>
								<td class="px-5 py-2.5 text-right">
									{#if confirmRevokeId === key.id}
										<div class="flex items-center justify-end gap-1">
											<button
												class="px-2 py-1 text-xs bg-accent-red text-white rounded hover:brightness-110 transition-all disabled:opacity-50"
												onclick={() => handleRevoke(key.id)}
												disabled={revoking}
											>
												{revoking ? '...' : 'Confirm'}
											</button>
											<button
												class="px-2 py-1 text-xs bg-bg-tertiary text-text-primary rounded hover:bg-border-active transition-all"
												onclick={() => (confirmRevokeId = null)}
											>
												Cancel
											</button>
										</div>
									{:else}
										<button
											class="px-2 py-1 text-xs text-accent-red hover:bg-accent-red/10 rounded transition-colors"
											onclick={() => (confirmRevokeId = key.id)}
										>
											Revoke
										</button>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</div>
</div>

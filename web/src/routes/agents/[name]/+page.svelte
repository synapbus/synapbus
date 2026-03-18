<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { agents as agentsApi, trust as trustApi } from '$lib/api/client';
	import TraceViewer from '$lib/components/TraceViewer.svelte';

	let agent = $state<any>(null);
	let traces = $state<any[]>([]);
	let loadingData = $state(true);
	let revokeKey = $state('');
	let revoking = $state(false);
	let deleting = $state(false);
	let confirmDelete = $state(false);

	// Inline name editing
	let editingName = $state(false);
	let editNameValue = $state('');
	let savingName = $state(false);
	let nameError = $state('');

	// Trust Scores state
	let trustScores = $state<Record<string, number>>({});
	let trustLoading = $state(false);
	let trustEntries = $derived(Object.entries(trustScores).sort(([a], [b]) => a.localeCompare(b)));

	// Access Rights state
	let allowedChannels = $state('');
	let readOnly = $state(false);
	let maxRate = $state(60);
	let savingAccess = $state(false);
	let accessSaved = $state(false);
	let accessError = $state('');

	let agentName = $derived($page.params.name);

	async function loadAgent() {
		loadingData = true;
		try {
			const res = await agentsApi.get(agentName);
			agent = res.agent;
			// Human agents cannot be managed via this page
			if (agent?.type === 'human') {
				goto('/agents');
				return;
			}
			traces = res.traces;
			// Populate access rights from capabilities
			const caps = agent.capabilities || {};
			allowedChannels = (caps.allowed_channels || []).join(', ');
			readOnly = caps.read_only ?? false;
			maxRate = caps.max_rate ?? 60;
			// Load trust scores
			loadTrustScores();
		} catch {
			// handled
		} finally {
			loadingData = false;
		}
	}

	async function loadTrustScores() {
		trustLoading = true;
		try {
			const res = await trustApi.get(agentName);
			trustScores = res.scores || {};
		} catch {
			trustScores = {};
		} finally {
			trustLoading = false;
		}
	}

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			loadAgent();
		}
	});

	async function handleRevokeKey() {
		revoking = true;
		revokeKey = '';
		try {
			const res = await agentsApi.revokeKey(agentName);
			revokeKey = res.api_key;
			agent = res.agent;
		} catch (err: any) {
			alert(err.message || 'Failed to revoke key');
		} finally {
			revoking = false;
		}
	}

	async function handleDelete() {
		deleting = true;
		try {
			await agentsApi.delete(agentName);
			goto('/agents');
		} catch (err: any) {
			alert(err.message || 'Failed to delete agent');
		} finally {
			deleting = false;
			confirmDelete = false;
		}
	}

	function startEditName() {
		editNameValue = agent.display_name || agent.name;
		editingName = true;
		nameError = '';
	}

	async function saveDisplayName() {
		if (!editNameValue.trim()) {
			nameError = 'Name cannot be empty';
			return;
		}
		savingName = true;
		nameError = '';
		try {
			const res = await agentsApi.update(agentName, { display_name: editNameValue.trim() });
			agent = res.agent;
			editingName = false;
		} catch (err: any) {
			nameError = err.message || 'Failed to update name';
		} finally {
			savingName = false;
		}
	}

	function handleNameKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') {
			e.preventDefault();
			saveDisplayName();
		} else if (e.key === 'Escape') {
			editingName = false;
		}
	}

	async function handleSaveAccess() {
		savingAccess = true;
		accessError = '';
		accessSaved = false;
		try {
			const channels = allowedChannels
				.split(',')
				.map(s => s.trim())
				.filter(s => s.length > 0);
			const capabilities = {
				...(agent.capabilities || {}),
				allowed_channels: channels,
				read_only: readOnly,
				max_rate: maxRate
			};
			const res = await agentsApi.update(agentName, { capabilities });
			agent = res.agent;
			accessSaved = true;
			setTimeout(() => (accessSaved = false), 3000);
		} catch (err: any) {
			accessError = err.message || 'Failed to save access rights';
		} finally {
			savingAccess = false;
		}
	}
</script>

<div class="p-5 max-w-5xl">
	<a href="/agents" class="inline-flex items-center gap-1 text-xs text-text-link hover:underline mb-4">
		<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
			<path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
		</svg>
		Back to agents
	</a>

	{#if loadingData}
		<div class="card p-6">
			<div class="flex gap-3">
				<div class="skeleton w-12 h-12 rounded-lg"></div>
				<div class="flex-1 space-y-2">
					<div class="skeleton h-5 w-1/3"></div>
					<div class="skeleton h-3 w-1/4"></div>
				</div>
			</div>
		</div>
	{:else if agent}
		<!-- Agent Details -->
		<div class="card mb-5">
			<div class="p-5 border-b border-border">
				<div class="flex items-start justify-between">
					<div class="flex items-center gap-3">
						<div class="w-12 h-12 rounded-lg bg-bg-tertiary flex items-center justify-center text-lg font-bold text-text-secondary">
							{(agent.display_name || agent.name).charAt(0).toUpperCase()}
						</div>
						<div>
							{#if editingName}
								<div class="flex items-center gap-2">
									<input
										type="text"
										class="input text-lg font-bold py-0.5 px-2 w-48"
										bind:value={editNameValue}
										onkeydown={handleNameKeydown}
										onblur={saveDisplayName}
										autofocus
									/>
									{#if savingName}
										<span class="text-xs text-text-secondary">Saving...</span>
									{/if}
								</div>
								{#if nameError}
									<p class="text-xs text-accent-red mt-0.5">{nameError}</p>
								{/if}
							{:else}
								<h1
									class="text-lg font-bold text-text-primary font-display cursor-pointer hover:text-accent-blue transition-colors"
									onclick={startEditName}
									title="Click to edit display name"
								>
									{agent.display_name || agent.name}
									<svg class="w-3.5 h-3.5 inline-block ml-1 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
										<path stroke-linecap="round" stroke-linejoin="round" d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
									</svg>
								</h1>
							{/if}
							<p class="text-xs text-text-secondary font-mono">@{agent.name}</p>
						</div>
					</div>
					<div class="flex items-center gap-2">
						<span class="badge {agent.type === 'ai' ? 'bg-accent-purple/20 text-accent-purple' : 'bg-accent-blue/20 text-accent-blue'}">
							{agent.type}
						</span>
						<span class="flex items-center gap-1.5 badge {agent.status === 'active' ? 'bg-accent-green/20 text-accent-green' : 'bg-bg-tertiary text-text-secondary'}">
							<span class="w-1.5 h-1.5 rounded-full {agent.status === 'active' ? 'bg-accent-green' : 'bg-text-secondary'}"></span>
							{agent.status}
						</span>
					</div>
				</div>
			</div>

			<div class="p-5 space-y-3">
				<div class="grid grid-cols-2 gap-4 text-sm">
					<div>
						<p class="text-xs text-text-secondary mb-0.5">Created</p>
						<p class="text-text-primary">{new Date(agent.created_at).toLocaleString()}</p>
					</div>
					<div>
						<p class="text-xs text-text-secondary mb-0.5">Last Updated</p>
						<p class="text-text-primary">{new Date(agent.updated_at).toLocaleString()}</p>
					</div>
				</div>

				{#if agent.capabilities && JSON.stringify(agent.capabilities) !== '{}'}
					<div>
						<p class="text-xs text-text-secondary mb-1">Capabilities</p>
						<pre class="text-xs bg-bg-primary p-3 rounded-lg border border-border overflow-x-auto font-mono text-text-primary/80">{JSON.stringify(agent.capabilities, null, 2)}</pre>
					</div>
				{/if}
			</div>

			<!-- Actions -->
			<div class="p-5 border-t border-border space-y-3">
				{#if revokeKey}
					<div class="p-4 bg-accent-green/10 border border-accent-green/20 rounded-lg">
						<p class="text-xs font-semibold text-accent-green mb-2">New API key generated. Save it now - it will not be shown again:</p>
						<code class="block p-3 bg-bg-primary rounded text-xs font-mono text-text-primary break-all select-all border border-border">{revokeKey}</code>
					</div>
				{/if}

				<div class="flex gap-2">
					<button class="btn-secondary text-xs" onclick={handleRevokeKey} disabled={revoking}>
						{revoking ? 'Regenerating...' : 'Regenerate API Key'}
					</button>
					{#if !confirmDelete}
						<button class="btn-danger text-xs" onclick={() => (confirmDelete = true)}>
							Delete Agent
						</button>
					{:else}
						<button class="btn-danger text-xs" onclick={handleDelete} disabled={deleting}>
							{deleting ? 'Deleting...' : 'Confirm Delete'}
						</button>
						<button class="btn-secondary text-xs" onclick={() => (confirmDelete = false)}>Cancel</button>
					{/if}
				</div>
			</div>
		</div>

		<!-- Webhook & K8s Management Links -->
		<div class="card mb-5">
			<div class="px-5 py-3 border-b border-border">
				<h2 class="font-semibold text-sm text-text-primary font-display">Event Handlers</h2>
			</div>
			<div class="p-5 flex gap-3">
				<a href="/agents/{agentName}/webhooks" class="btn-secondary text-xs inline-flex items-center gap-1.5">
					<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
						<path stroke-linecap="round" stroke-linejoin="round" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
					</svg>
					Webhooks
				</a>
				<a href="/agents/{agentName}/k8s-handlers" class="btn-secondary text-xs inline-flex items-center gap-1.5">
					<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
						<path stroke-linecap="round" stroke-linejoin="round" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
					</svg>
					K8s Handlers
				</a>
			</div>
		</div>

		<!-- Trust Scores -->
		<div class="card mb-5">
			<div class="px-5 py-3 border-b border-border">
				<h2 class="font-semibold text-sm text-text-primary font-display">Trust Scores</h2>
			</div>
			<div class="p-5">
				{#if trustLoading}
					<div class="space-y-3">
						<div class="skeleton h-4 w-1/2"></div>
						<div class="skeleton h-4 w-2/3"></div>
					</div>
				{:else if trustEntries.length === 0}
					<p class="text-xs text-text-secondary">No trust scores recorded yet.</p>
				{:else}
					<div class="space-y-3">
						{#each trustEntries as [actionType, score]}
							<div>
								<div class="flex items-center justify-between mb-1">
									<span class="text-xs font-medium text-text-primary">{actionType}</span>
									<span class="text-xs text-text-secondary">{Math.round(score * 100)}%</span>
								</div>
								<div class="w-full h-2 bg-bg-tertiary rounded-full overflow-hidden">
									<div
										class="h-full rounded-full transition-all duration-300 {score >= 0.7 ? 'bg-accent-green' : score >= 0.4 ? 'bg-accent-yellow' : 'bg-accent-red'}"
										style="width: {Math.round(score * 100)}%"
									></div>
								</div>
							</div>
						{/each}
					</div>
				{/if}
			</div>
		</div>

		<!-- Access Rights -->
		<div class="card mb-5">
			<div class="px-5 py-3 border-b border-border">
				<h2 class="font-semibold text-sm text-text-primary font-display">Access Rights</h2>
			</div>
			<div class="p-5 space-y-4">
				{#if accessError}
					<div class="px-3 py-2 bg-accent-red/10 rounded text-xs text-accent-red">{accessError}</div>
				{/if}
				{#if accessSaved}
					<div class="px-3 py-2 bg-accent-green/10 rounded text-xs text-accent-green">Access rights saved successfully.</div>
				{/if}

				<div>
					<label for="allowed-channels" class="block text-xs font-medium text-text-secondary mb-1">Allowed Channels</label>
					<input
						id="allowed-channels"
						type="text"
						class="input"
						placeholder="e.g. general, project-alpha"
						bind:value={allowedChannels}
					/>
					<p class="text-[10px] text-text-secondary mt-1">Comma-separated list of channel names this agent can access. Leave empty for all channels.</p>
				</div>

				<div class="flex items-center gap-3">
					<label class="flex items-center gap-2 cursor-pointer">
						<input
							type="checkbox"
							class="w-4 h-4 rounded border-border bg-bg-tertiary text-accent-blue focus:ring-accent-blue"
							bind:checked={readOnly}
						/>
						<span class="text-sm text-text-primary">Read-only mode</span>
					</label>
					<p class="text-[10px] text-text-secondary">Agent can read messages but cannot send.</p>
				</div>

				<div>
					<label for="max-rate" class="block text-xs font-medium text-text-secondary mb-1">Max Message Rate (per minute)</label>
					<input
						id="max-rate"
						type="number"
						class="input w-32"
						min="1"
						max="1000"
						bind:value={maxRate}
					/>
				</div>

				<button
					class="btn-primary text-xs"
					onclick={handleSaveAccess}
					disabled={savingAccess}
				>
					{savingAccess ? 'Saving...' : 'Save Access Rights'}
				</button>
			</div>
		</div>

		<!-- Activity Traces -->
		<div class="card">
			<div class="px-5 py-3 border-b border-border">
				<h2 class="font-semibold text-sm text-text-primary font-display">Activity Traces</h2>
			</div>
			<TraceViewer {traces} />
		</div>
	{:else}
		<div class="card p-8 text-center text-text-secondary text-sm">
			Agent not found
		</div>
	{/if}
</div>

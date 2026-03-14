<script lang="ts">
	import { page } from '$app/stores';
	import { k8sHandlers, k8sJobRuns } from '$lib/api/client';

	let handlers = $state<any[]>([]);
	let jobRuns = $state<any[]>([]);
	let loading = $state(true);
	let k8sAvailable = $state(false);
	let selectedHandler = $state<number | null>(null);
	let logsContent = $state('');
	let logsJobId = $state<number | null>(null);
	let loadingLogs = $state(false);

	let agentName = $derived($page.params.name);

	async function loadHandlers() {
		loading = true;
		try {
			const res = await k8sHandlers.list(agentName);
			handlers = res.handlers || [];
			k8sAvailable = res.k8s_available ?? false;
		} catch {
			// handled
		} finally {
			loading = false;
		}
	}

	async function loadJobRuns() {
		try {
			const res = await k8sJobRuns.list({ agent: agentName, limit: 50 });
			jobRuns = res.job_runs || [];
		} catch {
			jobRuns = [];
		}
	}

	async function viewLogs(runId: number) {
		loadingLogs = true;
		logsJobId = runId;
		logsContent = '';
		try {
			const res = await k8sJobRuns.logs(runId);
			logsContent = res.logs || '(no output)';
		} catch (err: any) {
			logsContent = `Error: ${err.message || 'Failed to fetch logs'}`;
		} finally {
			loadingLogs = false;
		}
	}

	let _initialized = $state(false);
	$effect(() => {
		if (!_initialized) {
			_initialized = true;
			loadHandlers();
			loadJobRuns();
		}
	});

	function statusBadge(status: string): string {
		switch (status) {
			case 'active': return 'bg-accent-green/20 text-accent-green';
			case 'disabled': return 'bg-accent-red/20 text-accent-red';
			case 'succeeded': return 'bg-accent-green/20 text-accent-green';
			case 'pending': return 'bg-accent-yellow/20 text-accent-yellow';
			case 'running': return 'bg-accent-blue/20 text-accent-blue';
			case 'failed': return 'bg-accent-red/20 text-accent-red';
			default: return 'bg-bg-tertiary text-text-secondary';
		}
	}
</script>

<div class="p-5 max-w-5xl">
	<a href="/agents/{agentName}" class="inline-flex items-center gap-1 text-xs text-text-link hover:underline mb-4">
		<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
			<path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
		</svg>
		Back to {agentName}
	</a>

	<h1 class="text-lg font-bold text-text-primary font-display mb-4">K8s Handlers for @{agentName}</h1>

	{#if !k8sAvailable && !loading}
		<div class="card p-4 mb-4 bg-accent-yellow/10 border-accent-yellow/20">
			<p class="text-xs text-accent-yellow">Kubernetes runner is not available. SynapBus is not running in a K8s cluster.</p>
		</div>
	{/if}

	{#if loading}
		<div class="card p-6">
			<div class="skeleton h-4 w-1/3 mb-3"></div>
			<div class="skeleton h-4 w-2/3"></div>
		</div>
	{:else if handlers.length === 0}
		<div class="card p-8 text-center text-text-secondary text-sm">
			No K8s handlers registered. Use the <code class="font-mono bg-bg-tertiary px-1 rounded">register_k8s_handler</code> MCP tool to add one.
		</div>
	{:else}
		<div class="space-y-3 mb-6">
			{#each handlers as h}
				<div class="card p-4">
					<div class="flex items-center justify-between mb-2">
						<div class="flex items-center gap-2">
							<span class="badge {statusBadge(h.status)}">{h.status}</span>
							<code class="text-xs font-mono text-text-primary">{h.image}</code>
						</div>
						<span class="text-[10px] text-text-secondary">ID: {h.id}</span>
					</div>
					<div class="flex flex-wrap gap-4 text-[10px] text-text-secondary">
						<span>Events: {(h.events || []).join(', ')}</span>
						<span>Namespace: {h.namespace || 'default'}</span>
						{#if h.resources_memory}
							<span>Mem: {h.resources_memory}</span>
						{/if}
						{#if h.resources_cpu}
							<span>CPU: {h.resources_cpu}</span>
						{/if}
						<span>Timeout: {h.timeout_seconds}s</span>
					</div>
				</div>
			{/each}
		</div>
	{/if}

	<!-- Job Runs -->
	<div class="card">
		<div class="px-5 py-3 border-b border-border">
			<h2 class="font-semibold text-sm text-text-primary font-display">Job Runs</h2>
		</div>
		{#if jobRuns.length === 0}
			<div class="p-6 text-center text-text-secondary text-xs">No job runs yet</div>
		{:else}
			<div class="overflow-x-auto">
				<table class="w-full text-xs">
					<thead>
						<tr class="border-b border-border text-text-secondary">
							<th class="px-4 py-2 text-left font-medium">Job Name</th>
							<th class="px-4 py-2 text-left font-medium">Status</th>
							<th class="px-4 py-2 text-left font-medium">Namespace</th>
							<th class="px-4 py-2 text-left font-medium">Message</th>
							<th class="px-4 py-2 text-left font-medium">Started</th>
							<th class="px-4 py-2 text-left font-medium">Actions</th>
						</tr>
					</thead>
					<tbody>
						{#each jobRuns as run}
							<tr class="border-b border-border/50 hover:bg-bg-tertiary/30">
								<td class="px-4 py-2 font-mono">{run.job_name}</td>
								<td class="px-4 py-2">
									<span class="badge {statusBadge(run.status)}">{run.status}</span>
								</td>
								<td class="px-4 py-2">{run.namespace}</td>
								<td class="px-4 py-2">#{run.message_id}</td>
								<td class="px-4 py-2 text-text-secondary">
									{run.started_at ? new Date(run.started_at).toLocaleString() : '-'}
								</td>
								<td class="px-4 py-2">
									{#if run.status === 'succeeded' || run.status === 'failed'}
										<button
											class="btn-secondary text-[10px] px-2 py-0.5"
											onclick={() => viewLogs(run.id)}
										>
											Logs
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

	<!-- Logs Modal -->
	{#if logsJobId !== null}
		<div class="card mt-4">
			<div class="px-5 py-3 border-b border-border flex items-center justify-between">
				<h2 class="font-semibold text-sm text-text-primary font-display">Job Logs</h2>
				<button class="text-xs text-text-secondary hover:text-text-primary" onclick={() => { logsJobId = null; logsContent = ''; }}>Close</button>
			</div>
			<div class="p-4">
				{#if loadingLogs}
					<div class="skeleton h-20 w-full"></div>
				{:else}
					<pre class="text-xs font-mono bg-bg-primary p-3 rounded-lg border border-border overflow-x-auto max-h-96 whitespace-pre-wrap">{logsContent}</pre>
				{/if}
			</div>
		</div>
	{/if}
</div>

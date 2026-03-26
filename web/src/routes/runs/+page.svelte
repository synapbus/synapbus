<script lang="ts">
	import { runs } from '$lib/api/client';
	import { user } from '$lib/stores/auth';

	let runsList = $state<any[]>([]);
	let total = $state(0);
	let reactiveAgents = $state<any[]>([]);
	let filterAgent = $state('');
	let filterStatus = $state('');
	let loading = $state(true);
	let expandedRun = $state<number | null>(null);
	let _intervalId: ReturnType<typeof setInterval> | null = null;
	let _initialized = $state(false);

	$effect(() => {
		if (!_initialized && $user) {
			_initialized = true;
			loadData();
			_intervalId = setInterval(loadData, 10000);
		}
		return () => {
			if (_intervalId) {
				clearInterval(_intervalId);
				_intervalId = null;
			}
		};
	});

	async function loadData() {
		try {
			const [runsRes, agentsRes] = await Promise.all([
				runs.list({ agent: filterAgent || undefined, status: filterStatus || undefined, limit: 50 }),
				runs.reactiveAgents()
			]);
			runsList = runsRes.runs ?? [];
			total = runsRes.total;
			reactiveAgents = agentsRes.agents ?? [];
		} catch (e) {
			console.error('Failed to load runs data:', e);
		}
		loading = false;
	}

	function statusColor(status: string): string {
		switch (status) {
			case 'succeeded': return 'var(--color-success, #22c55e)';
			case 'running': return 'var(--color-warning, #eab308)';
			case 'failed': return 'var(--color-error, #ef4444)';
			case 'queued': return 'var(--color-info, #3b82f6)';
			case 'cooldown_skipped': return '#94a3b8';
			case 'budget_exhausted': return '#f97316';
			case 'depth_exceeded': return '#a855f7';
			default: return '#6b7280';
		}
	}

	function formatDuration(ms: number | null): string {
		if (!ms) return '-';
		const secs = Math.floor(ms / 1000);
		if (secs < 60) return `${secs}s`;
		return `${Math.floor(secs / 60)}m${secs % 60}s`;
	}

	function formatTime(t: string): string {
		if (!t) return '-';
		const d = new Date(t);
		return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) + ' ' + d.toLocaleDateString([], { month: 'short', day: 'numeric' });
	}

	function triggerLabel(run: any): string {
		if (run.trigger_event === 'message.received') return `DM from ${run.trigger_from || 'unknown'}`;
		if (run.trigger_event === 'message.mentioned') return `@mention by ${run.trigger_from || 'unknown'}`;
		return run.trigger_event;
	}

	function stateColor(state: string): string {
		switch (state) {
			case 'idle': return '#22c55e';
			case 'running': return '#eab308';
			case 'queued': return '#3b82f6';
			case 'cooldown': return '#94a3b8';
			case 'budget_exhausted': return '#f97316';
			default: return '#6b7280';
		}
	}

	async function retryRun(id: number) {
		try {
			await runs.retry(id);
			await loadData();
		} catch (e: any) {
			alert(e.message || 'Retry failed');
		}
	}

	function toggleExpand(id: number) {
		expandedRun = expandedRun === id ? null : id;
	}
</script>

<svelte:head>
	<title>Agent Runs - SynapBus</title>
</svelte:head>

<div class="page-container">
	<h1>Agent Runs</h1>

	<!-- Agent summary cards -->
	{#if reactiveAgents.length > 0}
		<div class="agent-cards">
			{#each reactiveAgents as agent}
				<div class="agent-card">
					<div class="agent-card-header">
						<span class="agent-name">{agent.name}</span>
						<span class="state-badge" style="background:{stateColor(agent.state)}">{agent.state}</span>
					</div>
					<div class="agent-card-stats">
						<span class="stat">
							<span class="stat-value">{agent.today_runs}/{agent.daily_trigger_budget}</span>
							<span class="stat-label">runs today</span>
						</span>
						<span class="stat">
							<span class="stat-value">{agent.cooldown_seconds}s</span>
							<span class="stat-label">cooldown</span>
						</span>
					</div>
				</div>
			{/each}
		</div>
	{/if}

	<!-- Filters -->
	<div class="filters">
		<select bind:value={filterAgent} onchange={() => loadData()}>
			<option value="">All agents</option>
			{#each reactiveAgents as agent}
				<option value={agent.name}>{agent.name}</option>
			{/each}
		</select>
		<select bind:value={filterStatus} onchange={() => loadData()}>
			<option value="">All statuses</option>
			<option value="running">Running</option>
			<option value="succeeded">Succeeded</option>
			<option value="failed">Failed</option>
			<option value="queued">Queued</option>
			<option value="cooldown_skipped">Cooldown Skipped</option>
			<option value="budget_exhausted">Budget Exhausted</option>
			<option value="depth_exceeded">Depth Exceeded</option>
		</select>
		<span class="total-count">{total} runs</span>
	</div>

	<!-- Runs list -->
	{#if loading}
		<div class="loading">Loading...</div>
	{:else if runsList.length === 0}
		<div class="empty">No reactive runs found.</div>
	{:else}
		<div class="runs-list">
			{#each runsList as run}
				<div class="run-row" class:expanded={expandedRun === run.id}>
					<button class="run-row-main" onclick={() => toggleExpand(run.id)}>
						<span class="status-dot" style="background:{statusColor(run.status)}"></span>
						<span class="run-agent">{run.agent_name}</span>
						<span class="run-trigger">{triggerLabel(run)}</span>
						<span class="run-status">{run.status}</span>
						<span class="run-duration">{formatDuration(run.duration_ms)}</span>
						<span class="run-time">{formatTime(run.created_at)}</span>
						<span class="expand-arrow">{expandedRun === run.id ? '▼' : '▶'}</span>
					</button>

					{#if expandedRun === run.id}
						<div class="run-details">
							<div class="detail-row">
								<span class="detail-label">Run ID</span>
								<span class="detail-value">{run.id}</span>
							</div>
							<div class="detail-row">
								<span class="detail-label">K8s Job</span>
								<span class="detail-value">{run.k8s_job_name || '-'}</span>
							</div>
							<div class="detail-row">
								<span class="detail-label">Depth</span>
								<span class="detail-value">{run.trigger_depth}</span>
							</div>
							{#if run.trigger_message_id}
								<div class="detail-row">
									<span class="detail-label">Trigger Message</span>
									<a href="/dm/{run.trigger_from}?msg={run.trigger_message_id}" class="detail-link">
										Message #{run.trigger_message_id}
									</a>
								</div>
							{/if}
							{#if run.error_log}
								<div class="error-log">
									<div class="error-log-header">Error Log</div>
									<pre>{run.error_log}</pre>
								</div>
							{/if}
							{#if run.status === 'failed'}
								<button class="retry-btn" onclick={() => retryRun(run.id)}>
									Retry
								</button>
							{/if}
						</div>
					{/if}
				</div>
			{/each}
		</div>
	{/if}
</div>

<style>
	.page-container {
		max-width: 1000px;
		margin: 0 auto;
		padding: 1.5rem;
	}

	h1 {
		font-size: 1.5rem;
		font-weight: 600;
		margin-bottom: 1rem;
	}

	.agent-cards {
		display: flex;
		gap: 0.75rem;
		flex-wrap: wrap;
		margin-bottom: 1rem;
	}

	.agent-card {
		background: var(--color-surface, #1e293b);
		border: 1px solid var(--color-border, #334155);
		border-radius: 0.5rem;
		padding: 0.75rem 1rem;
		min-width: 180px;
		flex: 1;
	}

	.agent-card-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-bottom: 0.5rem;
	}

	.agent-name {
		font-weight: 600;
		font-size: 0.875rem;
	}

	.state-badge {
		font-size: 0.7rem;
		padding: 0.125rem 0.5rem;
		border-radius: 9999px;
		color: white;
		font-weight: 500;
	}

	.agent-card-stats {
		display: flex;
		gap: 1rem;
	}

	.stat {
		display: flex;
		flex-direction: column;
	}

	.stat-value {
		font-size: 0.875rem;
		font-weight: 600;
	}

	.stat-label {
		font-size: 0.7rem;
		color: var(--color-text-muted, #94a3b8);
	}

	.filters {
		display: flex;
		gap: 0.5rem;
		align-items: center;
		margin-bottom: 1rem;
	}

	.filters select {
		background: var(--color-surface, #1e293b);
		border: 1px solid var(--color-border, #334155);
		color: var(--color-text, #e2e8f0);
		padding: 0.375rem 0.75rem;
		border-radius: 0.375rem;
		font-size: 0.875rem;
	}

	.total-count {
		margin-left: auto;
		font-size: 0.8rem;
		color: var(--color-text-muted, #94a3b8);
	}

	.loading, .empty {
		text-align: center;
		padding: 3rem;
		color: var(--color-text-muted, #94a3b8);
	}

	.runs-list {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.run-row {
		background: var(--color-surface, #1e293b);
		border: 1px solid var(--color-border, #334155);
		border-radius: 0.375rem;
		overflow: hidden;
	}

	.run-row.expanded {
		border-color: var(--color-primary, #3b82f6);
	}

	.run-row-main {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding: 0.625rem 0.75rem;
		width: 100%;
		background: none;
		border: none;
		color: inherit;
		cursor: pointer;
		font-size: 0.8125rem;
		text-align: left;
	}

	.run-row-main:hover {
		background: var(--color-surface-hover, #334155);
	}

	.status-dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.run-agent {
		font-weight: 600;
		min-width: 140px;
	}

	.run-trigger {
		flex: 1;
		color: var(--color-text-muted, #94a3b8);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.run-status {
		min-width: 100px;
		font-size: 0.75rem;
	}

	.run-duration {
		min-width: 60px;
		text-align: right;
		font-variant-numeric: tabular-nums;
	}

	.run-time {
		min-width: 100px;
		text-align: right;
		color: var(--color-text-muted, #94a3b8);
		font-size: 0.75rem;
	}

	.expand-arrow {
		font-size: 0.625rem;
		color: var(--color-text-muted, #94a3b8);
	}

	.run-details {
		padding: 0.75rem 1rem;
		border-top: 1px solid var(--color-border, #334155);
		background: var(--color-surface-alt, #0f172a);
	}

	.detail-row {
		display: flex;
		gap: 1rem;
		padding: 0.25rem 0;
		font-size: 0.8125rem;
	}

	.detail-label {
		color: var(--color-text-muted, #94a3b8);
		min-width: 120px;
	}

	.detail-link {
		color: var(--color-primary, #3b82f6);
		text-decoration: none;
	}

	.detail-link:hover {
		text-decoration: underline;
	}

	.error-log {
		margin-top: 0.5rem;
	}

	.error-log-header {
		font-weight: 600;
		font-size: 0.8125rem;
		margin-bottom: 0.25rem;
		color: var(--color-error, #ef4444);
	}

	.error-log pre {
		background: #0a0a0a;
		color: #e2e8f0;
		padding: 0.75rem;
		border-radius: 0.375rem;
		font-size: 0.75rem;
		overflow-x: auto;
		max-height: 300px;
		overflow-y: auto;
		white-space: pre-wrap;
		word-break: break-word;
	}

	.retry-btn {
		margin-top: 0.5rem;
		padding: 0.375rem 1rem;
		background: var(--color-primary, #3b82f6);
		color: white;
		border: none;
		border-radius: 0.375rem;
		cursor: pointer;
		font-size: 0.8125rem;
		font-weight: 500;
	}

	.retry-btn:hover {
		opacity: 0.9;
	}
</style>

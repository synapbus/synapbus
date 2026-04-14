<script lang="ts">
	import { goals } from '$lib/api/client';
	import { user } from '$lib/stores/auth';

	let list = $state<any[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let _intervalId: ReturnType<typeof setInterval> | null = null;
	let _initialized = $state(false);

	$effect(() => {
		if (!_initialized && $user) {
			_initialized = true;
			void load();
			_intervalId = setInterval(load, 10_000);
		}
		return () => {
			if (_intervalId) clearInterval(_intervalId);
			_intervalId = null;
		};
	});

	async function load() {
		try {
			const r = await goals.list({ limit: 50 });
			list = r.goals ?? [];
			error = null;
		} catch (e: any) {
			error = e?.message ?? String(e);
		} finally {
			loading = false;
		}
	}

	function formatDollars(cents: number): string {
		return `$${(cents / 100).toFixed(2)}`;
	}

	function statusColor(s: string): string {
		switch (s) {
			case 'active':
				return 'bg-blue-500/20 text-blue-300 border-blue-500/40';
			case 'completed':
				return 'bg-green-500/20 text-green-300 border-green-500/40';
			case 'paused':
				return 'bg-yellow-500/20 text-yellow-300 border-yellow-500/40';
			case 'stuck':
				return 'bg-orange-500/20 text-orange-300 border-orange-500/40';
			case 'cancelled':
				return 'bg-red-500/20 text-red-300 border-red-500/40';
			default:
				return 'bg-bg-tertiary text-text-secondary border-border';
		}
	}
</script>

<svelte:head>
	<title>Goals — SynapBus</title>
</svelte:head>

<div class="p-6 max-w-6xl mx-auto">
	<header class="mb-6">
		<h1 class="text-2xl font-semibold text-text-primary">Goals</h1>
		<p class="text-sm text-text-secondary mt-1">
			Top-level objectives owned by a human, decomposed into task trees by a coordinator, and
			executed by dynamically spawned specialist agents.
		</p>
	</header>

	{#if loading}
		<p class="text-text-secondary">Loading…</p>
	{:else if error}
		<div class="rounded border border-red-500/40 bg-red-500/10 p-3 text-red-300 text-sm">
			{error}
		</div>
	{:else if list.length === 0}
		<div class="rounded border border-border bg-bg-secondary p-6 text-center text-text-secondary">
			No goals yet. Goals are created by the coordinator agent when a human DMs it with a brief.
		</div>
	{:else}
		<div class="space-y-3">
			{#each list as g (g.id)}
				<a
					href={`/goals/${g.id}`}
					class="block rounded-lg border border-border bg-bg-secondary p-4 hover:border-text-link transition-colors"
				>
					<div class="flex items-start justify-between gap-4">
						<div class="min-w-0 flex-1">
							<div class="flex items-center gap-2 mb-1">
								<span class="text-xs font-mono text-text-tertiary">#{g.id}</span>
								<span
									class="text-xs rounded px-2 py-0.5 border uppercase tracking-wide {statusColor(
										g.status
									)}"
								>
									{g.status}
								</span>
								<span class="text-xs text-text-secondary">by {g.owner_username || '—'}</span>
							</div>
							<h2 class="font-medium text-text-primary truncate">{g.title}</h2>
							<div class="text-xs font-mono text-text-tertiary mt-1">#goal-{g.slug}</div>
						</div>
						<div class="text-right shrink-0">
							<div class="text-xs text-text-secondary">Spend</div>
							<div class="text-sm font-semibold text-text-primary">
								{formatDollars(g.spent_dollars_cents)}
							</div>
							<div class="text-xs text-text-tertiary">{g.spent_tokens.toLocaleString()} tok</div>
							{#if g.budget_dollars_cents}
								<div class="mt-1 text-xs text-text-secondary">
									{g.percent_budget.toFixed(0)}% of {formatDollars(g.budget_dollars_cents)}
								</div>
								<div class="mt-1 h-1 w-24 rounded bg-bg-tertiary overflow-hidden">
									<div
										class="h-full {g.percent_budget >= 100
											? 'bg-red-500'
											: g.percent_budget >= 80
												? 'bg-yellow-500'
												: 'bg-green-500'}"
										style={`width: ${Math.min(100, g.percent_budget).toFixed(1)}%`}
									></div>
								</div>
							{/if}
						</div>
					</div>
					<div class="mt-3 flex items-center gap-4 text-xs text-text-secondary">
						<span>{g.task_count} tasks</span>
						<span class="text-text-tertiary">·</span>
						<span>created {g.created_at.replace('T', ' ').replace('Z', ' UTC')}</span>
					</div>
				</a>
			{/each}
		</div>
	{/if}
</div>

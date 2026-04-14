<script lang="ts">
	import { page } from '$app/stores';
	import { goals } from '$lib/api/client';
	import { user } from '$lib/stores/auth';

	let data = $state<any>(null);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let _intervalId: ReturnType<typeof setInterval> | null = null;
	let _initialized = $state(false);

	const goalId = $derived(Number($page.params.id));

	$effect(() => {
		if (!_initialized && $user && goalId) {
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
			data = await goals.get(goalId);
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

	function statusPill(s: string): string {
		switch (s) {
			case 'done':
				return 'bg-green-500/20 text-green-300 border-green-500/40';
			case 'failed':
				return 'bg-red-500/20 text-red-300 border-red-500/40';
			case 'in_progress':
				return 'bg-blue-500/20 text-blue-300 border-blue-500/40';
			case 'awaiting_verification':
				return 'bg-purple-500/20 text-purple-300 border-purple-500/40';
			case 'claimed':
				return 'bg-indigo-500/20 text-indigo-300 border-indigo-500/40';
			case 'approved':
				return 'bg-cyan-500/20 text-cyan-300 border-cyan-500/40';
			default:
				return 'bg-bg-tertiary text-text-secondary border-border';
		}
	}

	// Build a parent→children lookup for tree rendering.
	const tree = $derived.by(() => {
		if (!data?.tasks) return { root: null, byParent: new Map() };
		const byParent = new Map<number | null, any[]>();
		let root: any = null;
		for (const t of data.tasks) {
			const key = t.parent_task_id ?? null;
			if (!byParent.has(key)) byParent.set(key, []);
			byParent.get(key)!.push(t);
			if (t.parent_task_id === null || t.parent_task_id === undefined) root = t;
		}
		return { root, byParent };
	});
</script>

<svelte:head>
	<title>{data?.goal?.title ?? 'Goal'} — SynapBus</title>
</svelte:head>

<div class="p-6 max-w-6xl mx-auto">
	<a href="/goals" class="inline-flex items-center gap-1 text-xs text-text-link hover:underline mb-4">
		← All goals
	</a>

	{#if loading && !data}
		<p class="text-text-secondary">Loading…</p>
	{:else if error}
		<div class="rounded border border-red-500/40 bg-red-500/10 p-3 text-red-300 text-sm">
			{error}
		</div>
	{:else if data}
		<header class="mb-6">
			<div class="flex items-center gap-2 mb-2">
				<span class="text-xs font-mono text-text-tertiary">#{data.goal.id}</span>
				<span class="text-xs rounded px-2 py-0.5 border uppercase tracking-wide {statusPill(
					data.goal.status
				)}">
					{data.goal.status}
				</span>
				<span class="text-xs text-text-secondary">by {data.goal.owner_username}</span>
			</div>
			<h1 class="text-2xl font-semibold text-text-primary">{data.goal.title}</h1>
			<div class="text-xs font-mono text-text-tertiary mt-1">#goal-{data.goal.slug}</div>
			<p class="text-sm text-text-secondary mt-3 whitespace-pre-wrap">{data.goal.description}</p>
		</header>

		<!-- Cost rollup + billing breakdown + spawned agents -->
		<section class="grid grid-cols-1 md:grid-cols-3 gap-4 mb-6">
			<div class="rounded-lg border border-border bg-bg-secondary p-4">
				<div class="text-xs uppercase text-text-tertiary tracking-wide">Total spend</div>
				<div class="text-xl font-semibold text-text-primary mt-1">
					{formatDollars(data.rollup.dollars_cents)}
				</div>
				<div class="text-xs text-text-secondary">
					{data.rollup.tokens.toLocaleString()} tokens · {data.rollup.task_count} tasks
				</div>
				{#if data.goal.budget_dollars_cents}
					<div class="mt-2 text-xs text-text-secondary">
						budget {formatDollars(data.goal.budget_dollars_cents)} · max depth
						{data.goal.max_spawn_depth}
					</div>
				{/if}
			</div>
			<div class="rounded-lg border border-border bg-bg-secondary p-4">
				<div class="text-xs uppercase text-text-tertiary tracking-wide">Billing breakdown</div>
				<ul class="mt-2 space-y-1 text-xs">
					{#each Object.entries(data.billing_breakdown) as [code, s] (code)}
						<li class="flex items-center justify-between">
							<span class="font-mono text-text-secondary">{code || '—'}</span>
							<span class="text-text-primary">{formatDollars((s as any).cents)}</span>
						</li>
					{/each}
				</ul>
			</div>
			<div class="rounded-lg border border-border bg-bg-secondary p-4">
				<div class="text-xs uppercase text-text-tertiary tracking-wide">Spawned agents</div>
				<ul class="mt-2 space-y-1 text-xs">
					{#each data.spawned_agents as a (a.id)}
						<li class="flex items-center justify-between gap-2">
							<a class="text-text-link hover:underline font-mono" href={`/agents/${a.name}`}>
								{a.name}
							</a>
							<span class="text-text-tertiary">depth {a.spawn_depth}</span>
						</li>
					{/each}
				</ul>
			</div>
		</section>

		<!-- Task tree -->
		<section class="mb-6">
			<h2 class="text-sm font-semibold text-text-primary mb-2">Task tree</h2>
			<div class="space-y-2">
				{#each data.tasks as t (t.id)}
					<div
						class="rounded border border-border bg-bg-secondary p-3"
						style={`margin-left: ${t.depth * 20}px`}
					>
						<div class="flex items-center gap-2">
							<span class="text-xs font-mono text-text-tertiary">#{t.id}</span>
							<span class="text-xs rounded px-2 py-0.5 border uppercase tracking-wide {statusPill(
								t.status
							)}">
								{t.status.replace('_', ' ')}
							</span>
							{#if t.billing_code}
								<span class="text-xs font-mono text-text-tertiary">{t.billing_code}</span>
							{/if}
							{#if t.assignee_agent_name}
								<span class="text-xs text-text-secondary">
									→
									<a href={`/agents/${t.assignee_agent_name}`} class="text-text-link hover:underline"
										>{t.assignee_agent_name}</a
									>
								</span>
							{/if}
						</div>
						<div class="mt-1 font-medium text-text-primary text-sm">{t.title}</div>
						{#if t.description}
							<div class="mt-1 text-xs text-text-secondary">{t.description}</div>
						{/if}
						{#if t.acceptance_criteria}
							<div class="mt-1 text-xs text-text-tertiary italic">
								acceptance: {t.acceptance_criteria}
							</div>
						{/if}
						<div class="mt-2 flex items-center gap-3 text-xs text-text-secondary">
							<span>{t.spent_tokens.toLocaleString()} tok</span>
							<span class="text-text-tertiary">·</span>
							<span>{formatDollars(t.spent_dollars_cents)}</span>
							{#if t.failure_reason}
								<span class="text-red-300">· {t.failure_reason}</span>
							{/if}
						</div>
					</div>
				{/each}
			</div>
		</section>

		<!-- Timeline -->
		{#if data.timeline?.length > 0}
			<section>
				<h2 class="text-sm font-semibold text-text-primary mb-2">Recent activity</h2>
				<div class="rounded-lg border border-border bg-bg-secondary p-4 max-h-96 overflow-y-auto">
					<ul class="space-y-2 text-xs">
						{#each data.timeline as ev (ev.id)}
							<li class="flex gap-2">
								<span class="font-mono text-text-tertiary shrink-0">
									{ev.created_at.substring(11, 19)}
								</span>
								<span class="font-mono text-text-secondary shrink-0">{ev.from}</span>
								{#if ev.kind}
									<span
										class="text-[10px] rounded px-1 bg-bg-tertiary text-text-tertiary uppercase shrink-0"
										>{ev.kind}</span
									>
								{/if}
								<span class="text-text-primary whitespace-pre-wrap break-words">{ev.body}</span>
							</li>
						{/each}
					</ul>
				</div>
			</section>
		{/if}
	{/if}
</div>
